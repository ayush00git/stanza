package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ayush00git/stanza/models"
)

const (
	chemblMoleculeAPI     = "https://www.ebi.ac.uk/chembl/api/data/molecule.json"
	chemblFetchLimit      = 600              // candidate pool fetched across pages before filtering
	chemblPageSize        = 200              // molecules per ChEMBL request (paginated by offset)
	chemblMaxResults      = 100              // max fragments returned to the client
	chemblValidateWorkers = 16               // concurrent OpenBabel SMILES checks
	chemblHTTPTimeout     = 20 * time.Second // covers paginated fetch + concurrent validation
	mwScalingFactor       = 3.0              // pocket volume (Å³) / factor → max MW (Da) cap
	logpTolerance         = 1.0
	polarTolerance        = 2.0 // fpocket polarity score maps loosely to H-bond counts
	obabelCheckTime       = 5 * time.Second
)

var chemblHTTPClient = &http.Client{Timeout: chemblHTTPTimeout}

var (
	chemblPocketCacheMu sync.RWMutex
	chemblPocketCache   = map[string][]models.Fragment{}
)

// pocketCacheKey identifies a pocket plus its geometric/chemical fingerprint for caching.
func pocketCacheKey(p models.Pocket) string {
	return fmt.Sprintf("%d:%.5g:%.5g:%.5g", p.PocketID, p.Volume, p.Hydrophobicity, p.Polarity)
}

// maxMWFromPocket maps pocket volume to a fragment MW ceiling (Daltons), clamped for fragment-likeness.
func maxMWFromPocket(volume float64) float64 {
	if volume <= 0 {
		return 300
	}
	mw := volume / mwScalingFactor
	if mw < 120 {
		mw = 120
	}
	if mw > 300 {
		mw = 300
	}
	return mw
}

// pocketMatchingTargets derives comparison targets from fpocket scores (hydrophobicity / polarity can be negative).
func pocketMatchingTargets(p models.Pocket) (targetLogP, targetPolarHB float64) {
	targetLogP = p.Hydrophobicity*2.0 + 1.0
	targetPolarHB = 4.0 + p.Polarity*6.0
	return targetLogP, targetPolarHB
}

type chemblMoleculeJSON struct {
	MoleculeChemblID   string  `json:"molecule_chembl_id"`
	PrefName           *string `json:"pref_name"`
	MoleculeProperties *struct {
		Alogp    string `json:"alogp"`
		FullMwt  string `json:"full_mwt"`
		HBA      int    `json:"hba"`
		HBD      int    `json:"hbd"`
		RTB      int    `json:"rtb"`
	} `json:"molecule_properties"`
	MoleculeStructures *struct {
		CanonicalSmiles string `json:"canonical_smiles"`
	} `json:"molecule_structures"`
	MoleculeSynonyms []struct {
		MoleculeSynonym string `json:"molecule_synonym"`
		SynonymType     string `json:"synonym_type"`
	} `json:"molecule_synonyms"`
}

type chemblListResponse struct {
	Molecules []chemblMoleculeJSON `json:"molecules"`
}

func parseChemblFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// estimateMW returns a rough molecular weight (Da) from SMILES when ChEMBL fields are missing.
func estimateMW(smiles string) float64 {
	if smiles == "" {
		return 0
	}
	n := len(strings.TrimSpace(smiles))
	if n == 0 {
		return 0
	}
	// Very coarse organic average (~C10H-area): enough for ranking/fallback only.
	return math.Min(300, math.Max(100, float64(n)*11))
}

// estimateLogP returns a neutral prior LogP when ChEMBL alogp is missing.
func estimateLogP(smiles string) float64 {
	if smiles == "" {
		return 0
	}
	_ = smiles
	return 1.5
}

func chemblMoleculeToFragment(m chemblMoleculeJSON) models.Fragment {
	var f models.Fragment
	var hasMW, hasAlogp bool
	f.ChemblID = m.MoleculeChemblID
	
	// Prioritize Preferred Name, then synonyms, then ID
	if m.PrefName != nil && strings.TrimSpace(*m.PrefName) != "" {
		f.Name = strings.TrimSpace(*m.PrefName)
	} else if len(m.MoleculeSynonyms) > 0 {
		// Use the first synonym if no preferred name
		f.Name = strings.TrimSpace(m.MoleculeSynonyms[0].MoleculeSynonym)
	} else {
		f.Name = m.MoleculeChemblID
	}

	if m.MoleculeStructures != nil {
		f.SMILES = strings.TrimSpace(m.MoleculeStructures.CanonicalSmiles)
	}
	if m.MoleculeProperties != nil {
		if mw, ok := parseChemblFloat(m.MoleculeProperties.FullMwt); ok {
			f.MolWeight = mw
			hasMW = true
		}
		if lp, ok := parseChemblFloat(m.MoleculeProperties.Alogp); ok {
			f.LogP = lp
			hasAlogp = true
		}
	}
	if !hasMW && f.SMILES != "" {
		f.MolWeight = estimateMW(f.SMILES)
	}
	if !hasAlogp && f.SMILES != "" {
		f.LogP = estimateLogP(f.SMILES)
	}
	return f
}

func smilesPassesOpenBabel(smiles string) bool {
	if smiles == "" {
		return false
	}
	path, err := exec.LookPath("obabel")
	if err != nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), obabelCheckTime)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-:"+smiles, "-osmi", "-N", "1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[chembl] obabel rejected SMILES: %v (%s)", err, strings.TrimSpace(stderr.String()))
		return false
	}
	return true
}

type scoredFrag struct {
	f     models.Fragment
	score float64
}

func matchScore(f models.Fragment, maxMW, targetLogP, targetPolarHB float64, hba, hbd int) float64 {
	hbSum := float64(hba + hbd)
	dLogP := math.Abs(f.LogP - targetLogP)
	dPol := math.Abs(hbSum - targetPolarHB)
	dMW := 0.0
	if f.MolWeight > maxMW {
		dMW = f.MolWeight - maxMW
	}
	return dLogP + 0.15*dPol + 0.02*dMW
}

func filterAndRankChembl(raw []chemblMoleculeJSON, pocket models.Pocket) []models.Fragment {
	maxMW := maxMWFromPocket(pocket.Volume)
	targetLogP, targetPolarHB := pocketMatchingTargets(pocket)

	var scored []scoredFrag
	for _, m := range raw {
		if m.MoleculeProperties == nil || m.MoleculeStructures == nil {
			continue
		}
		smiles := strings.TrimSpace(m.MoleculeStructures.CanonicalSmiles)
		if smiles == "" {
			continue
		}
		f := chemblMoleculeToFragment(m)
		if f.MolWeight > maxMW {
			continue
		}
		hba, hbd := m.MoleculeProperties.HBA, m.MoleculeProperties.HBD
		if math.Abs(f.LogP-targetLogP) > logpTolerance {
			continue
		}
		hbSum := float64(hba + hbd)
		if math.Abs(hbSum-targetPolarHB) > polarTolerance {
			continue
		}
		scored = append(scored, scoredFrag{
			f:     f,
			score: matchScore(f, maxMW, targetLogP, targetPolarHB, hba, hbd),
		})
	}

	if len(scored) == 0 {
		return relaxFilterAndRank(raw, maxMW, targetLogP, targetPolarHB)
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].f.MolWeight < scored[j].f.MolWeight
		}
		return scored[i].score < scored[j].score
	})
	out := make([]models.Fragment, 0, chemblMaxResults)
	for i := 0; i < len(scored) && len(out) < chemblMaxResults; i++ {
		out = append(out, scored[i].f)
	}
	return out
}

func relaxFilterAndRank(raw []chemblMoleculeJSON, maxMW, targetLogP, targetPolarHB float64) []models.Fragment {
	type cand struct {
		f     models.Fragment
		score float64
	}
	var all []cand
	for _, m := range raw {
		if m.MoleculeProperties == nil || m.MoleculeStructures == nil {
			continue
		}
		smiles := strings.TrimSpace(m.MoleculeStructures.CanonicalSmiles)
		if smiles == "" {
			continue
		}
		f := chemblMoleculeToFragment(m)
		if f.MolWeight > maxMW {
			continue
		}
		hba, hbd := m.MoleculeProperties.HBA, m.MoleculeProperties.HBD
		s := matchScore(f, maxMW, targetLogP, targetPolarHB, hba, hbd)
		all = append(all, cand{f: f, score: s})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score == all[j].score {
			return all[i].f.MolWeight < all[j].f.MolWeight
		}
		return all[i].score < all[j].score
	})
	out := make([]models.Fragment, 0, chemblMaxResults)
	for i := 0; i < len(all) && len(out) < chemblMaxResults; i++ {
		out = append(out, all[i].f)
	}
	return out
}

func buildChemblSearchURL(maxMW float64, limit, offset int) string {
	v := url.Values{}
	v.Set("limit", strconv.Itoa(limit))
	v.Set("offset", strconv.Itoa(offset))
	v.Set("molecule_properties__full_mwt__lte", formatChemblFloat(maxMW))
	return chemblMoleculeAPI + "?" + v.Encode()
}

func formatChemblFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func fetchChemblMoleculePage(ctx context.Context, u string) ([]chemblMoleculeJSON, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := chemblHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chembl HTTP %d: %s", resp.StatusCode, string(body))
	}
	var parsed chemblListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Molecules, nil
}

// queryChEMBL retrieves fragment-like ChEMBL molecules, scores them against
// pocket descriptors, and returns up to chemblMaxResults hits. The candidate
// pool is fetched across several pages concurrently, then SMILES validity is
// checked in parallel before scoring.
func queryChEMBL(pocket models.Pocket) []models.Fragment {
	maxMW := maxMWFromPocket(pocket.Volume)

	ctx, cancel := context.WithTimeout(context.Background(), chemblHTTPTimeout)
	defer cancel()

	// Fetch the candidate pool in pages, concurrently — offsets are known up
	// front, so there's no need to walk page_meta.next sequentially.
	pages := (chemblFetchLimit + chemblPageSize - 1) / chemblPageSize
	pageResults := make([][]chemblMoleculeJSON, pages)
	var fetchWg sync.WaitGroup
	for p := range pages {
		fetchWg.Add(1)
		go func(p int) {
			defer fetchWg.Done()
			u := buildChemblSearchURL(maxMW, chemblPageSize, p*chemblPageSize)
			mols, err := fetchChemblMoleculePage(ctx, u)
			if err != nil {
				log.Printf("[chembl] fetch page %d failed: %v", p, err)
				return
			}
			pageResults[p] = mols
		}(p)
	}
	fetchWg.Wait()

	var mols []chemblMoleculeJSON
	for _, page := range pageResults {
		mols = append(mols, page...)
	}
	if len(mols) == 0 {
		return nil
	}

	mols = prevalidateSMILES(ctx, mols)

	out := filterAndRankChembl(mols, pocket)
	if len(out) == 0 {
		log.Printf("[chembl] no molecules matched pocket %d after filtering", pocket.PocketID)
	}
	return out
}

// prevalidateSMILES runs OpenBabel validity checks concurrently — the per-molecule
// obabel subprocess is by far the slowest step, so fanning it out keeps a large
// candidate pool fast. Returns only molecules with a non-empty, accepted SMILES,
// preserving input order.
func prevalidateSMILES(ctx context.Context, raw []chemblMoleculeJSON) []chemblMoleculeJSON {
	valid := make([]bool, len(raw))
	sem := make(chan struct{}, chemblValidateWorkers)
	var wg sync.WaitGroup

	for i, m := range raw {
		if m.MoleculeProperties == nil || m.MoleculeStructures == nil {
			continue
		}
		smiles := strings.TrimSpace(m.MoleculeStructures.CanonicalSmiles)
		if smiles == "" {
			continue
		}
		wg.Add(1)
		go func(i int, s string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			valid[i] = smilesPassesOpenBabel(s)
		}(i, smiles)
	}
	wg.Wait()

	out := make([]chemblMoleculeJSON, 0, len(raw))
	for i, ok := range valid {
		if ok {
			out = append(out, raw[i])
		}
	}
	return out
}

// FetchFragments returns up to chemblMaxResults ChEMBL fragments tailored to the
// pocket. Never returns an error; failures yield an empty slice. Results are
// cached per pocket fingerprint.
func FetchFragments(pocket models.Pocket) []models.Fragment {
	key := pocketCacheKey(pocket)

	chemblPocketCacheMu.RLock()
	if cached, ok := chemblPocketCache[key]; ok {
		chemblPocketCacheMu.RUnlock()
		return append([]models.Fragment(nil), cached...)
	}
	chemblPocketCacheMu.RUnlock()

	frags := queryChEMBL(pocket)
	if len(frags) == 0 {
		return []models.Fragment{}
	}

	chemblPocketCacheMu.Lock()
	chemblPocketCache[key] = append([]models.Fragment(nil), frags...)
	chemblPocketCacheMu.Unlock()

	return append([]models.Fragment(nil), frags...)
}
