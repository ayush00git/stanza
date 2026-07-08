package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ayush00git/stanza/models"
)

// 30s timeout mirrors alphafoldClient / uniprotClient: it absorbs cold-start
// latency (DNS + TLS handshake) on the first outbound PDBe request so a cold
// first lookup doesn't fail before the connection warms up.
var pdbeClient = &http.Client{Timeout: 30 * time.Second}

// PDBe REST API base. The SIFTS best_structures endpoint returns experimental
// PDB structures for a UniProt accession, already ranked best-first (by observed
// residue coverage and resolution), and ligand_monomers reports bound ligands.
const pdbeAPIBase = "https://www.ebi.ac.uk/pdbe/api"

// rcsbDownloadBase is where we point StructureURL for the chosen entry. We serve
// mmCIF (files.rcsb.org/download/<ID>.cif) to match the AlphaFold CIF pathway.
const rcsbDownloadBase = "https://files.rcsb.org/download"

// maxLigandProbes caps how many top covering candidates we probe for ligands.
// SIFTS returns them best-first, so the best holo structure is almost always
// near the top; capping bounds latency (one extra HTTP call per probe).
const maxLigandProbes = 5

// crystallizationLigands are chem_comp_ids that are NOT drug-like: water plus
// the common crystallization ions, buffers, and cryoprotectants. A structure
// whose only "ligands" are these is treated as apo (unbound). Keys are upper-case
// because we upper-case each chem_comp_id before lookup.
var crystallizationLigands = map[string]bool{
	"HOH": true, // water
	"NA":  true, // sodium
	"CL":  true, // chloride
	"K":   true, // potassium
	"MG":  true, // magnesium
	"CA":  true, // calcium
	"ZN":  true, // zinc
	"SO4": true, // sulfate
	"PO4": true, // phosphate
	"GOL": true, // glycerol
	"EDO": true, // ethylene glycol
	"ACT": true, // acetate
	"DMS": true, // dimethyl sulfoxide
	"PEG": true, // polyethylene glycol
	"MPD": true, // 2-methyl-2,4-pentanediol
	"IOD": true, // iodide
	"BR":  true, // bromide
}

// siftsBestStructure is one entry from the PDBe best_structures (SIFTS) response.
// Only the fields we use are mapped. unp_start/unp_end delimit the UniProt range
// this chain resolves, so the mutated residue is present iff it falls in [start,end].
type siftsBestStructure struct {
	PDBID              string  `json:"pdb_id"`
	ChainID            string  `json:"chain_id"` // auth chain identifier
	Resolution         float64 `json:"resolution"`
	Coverage           float64 `json:"coverage"`
	UnpStart           int     `json:"unp_start"`
	UnpEnd             int     `json:"unp_end"`
	ExperimentalMethod string  `json:"experimental_method"`
}

// ligandMonomer is one bound ligand from the PDBe ligand_monomers response.
type ligandMonomer struct {
	ChemCompID string `json:"chem_comp_id"`
}

// FindBestExperimentalStructure returns the best experimental PDB structure for a
// UniProt accession that COVERS the given (1-based UniProt) position, preferring a
// holo (ligand-bound) structure over apo, then better resolution. Returns (nil, nil)
// when no suitable experimental structure exists (caller falls back to AlphaFold).
// Returns an error only on a transport/parse failure the caller should surface.
//
// wildType is accepted for signature symmetry with the caller; this function does
// NOT set WildTypeMatches (the caller sets it from the canonical UniProt sequence).
func FindBestExperimentalStructure(ctx context.Context, uniprotID string, position int, wildType string) (*models.WTStructure, error) {
	uniprotID = strings.TrimSpace(uniprotID)
	// A missing accession or non-positive position can never match a covering
	// residue, so there's nothing experimental to find — fall back to AlphaFold.
	if uniprotID == "" || position <= 0 {
		return nil, nil
	}

	// 1. Ranked candidates covering the residue (SIFTS best_structures).
	// A failed/empty mapping is not an error worth surfacing: the caller simply
	// has no experimental structure and falls back to AlphaFold, so return (nil,nil).
	mappingURL := fmt.Sprintf("%s/mappings/best_structures/%s", pdbeAPIBase, uniprotID)
	body, err := pdbeGET(ctx, mappingURL)
	if err != nil {
		return nil, nil
	}
	var mapping map[string][]siftsBestStructure
	if err := json.Unmarshal(body, &mapping); err != nil {
		return nil, nil
	}
	candidates := mapping[uniprotID]
	if len(candidates) == 0 {
		return nil, nil
	}

	// Keep only candidates whose resolved UniProt range spans the mutated residue,
	// so the position we care about is actually present in the structure. SIFTS
	// already returns them best-first, so we preserve that order.
	var covering []siftsBestStructure
	for _, c := range candidates {
		if c.PDBID == "" {
			continue
		}
		if c.UnpStart <= position && position <= c.UnpEnd {
			covering = append(covering, c)
		}
	}
	if len(covering) == 0 {
		return nil, nil
	}

	// 2. Holo vs apo. Probe the top few covering candidates for drug-like ligands.
	// Track the best holo candidate (most ligands, then best resolution) and the
	// best apo candidate (best resolution, SIFTS rank as tie-break).
	var (
		bestHolo      *siftsBestStructure
		bestHoloCount int
	)
	probes := len(covering)
	if probes > maxLigandProbes {
		probes = maxLigandProbes
	}
	for i := 0; i < probes; i++ {
		count := countDrugLikeLigands(ctx, covering[i].PDBID)
		if count <= 0 {
			continue // apo (or ligand lookup failed) — not a holo candidate
		}
		// Prefer more ligands; on a tie prefer better (lower) resolution.
		if bestHolo == nil || count > bestHoloCount ||
			(count == bestHoloCount && resolutionBetter(covering[i].Resolution, bestHolo.Resolution)) {
			c := covering[i]
			bestHolo = &c
			bestHoloCount = count
		}
	}

	// 3. Selection: a covering holo wins; otherwise the best covering apo.
	res := &models.WTStructure{
		ResidueResolved: true, // guaranteed: we filtered to covering candidates
		// WildTypeMatches intentionally left false — the caller sets it.
	}
	if bestHolo != nil {
		res.Source = models.SourcePDBHolo
		res.PDBID = strings.ToUpper(bestHolo.PDBID)
		res.Chain = bestHolo.ChainID
		res.Resolution = bestHolo.Resolution
		res.LigandCount = bestHoloCount
		res.Notes = buildNotes(*bestHolo, position, bestHoloCount, true)
	} else {
		// No holo among probed candidates: take the best covering apo. Iterate in
		// SIFTS order and keep the best resolution, so ties favor the higher rank.
		bestApo := covering[0]
		for _, c := range covering[1:] {
			if resolutionBetter(c.Resolution, bestApo.Resolution) {
				bestApo = c
			}
		}
		res.Source = models.SourcePDBApo
		res.PDBID = strings.ToUpper(bestApo.PDBID)
		res.Chain = bestApo.ChainID
		res.Resolution = bestApo.Resolution
		res.LigandCount = 0
		res.Notes = buildNotes(bestApo, position, 0, false)
	}
	res.StructureURL = fmt.Sprintf("%s/%s.cif", rcsbDownloadBase, res.PDBID)
	return res, nil
}

// countDrugLikeLigands returns the number of DISTINCT drug-like ligands bound in a
// PDB entry, excluding water and common crystallization ions/buffers. A count > 0
// means the entry is holo. A 404/empty body or any lookup failure yields 0, so the
// candidate is simply treated as apo rather than aborting the whole search.
func countDrugLikeLigands(ctx context.Context, pdbID string) int {
	pdbID = strings.ToLower(strings.TrimSpace(pdbID))
	if pdbID == "" {
		return 0
	}
	ligandURL := fmt.Sprintf("%s/pdb/entry/ligand_monomers/%s", pdbeAPIBase, pdbID)
	body, err := pdbeGET(ctx, ligandURL)
	if err != nil {
		return 0 // no ligand data (often a 404) → treat as apo
	}
	var mapping map[string][]ligandMonomer
	if err := json.Unmarshal(body, &mapping); err != nil {
		return 0
	}
	seen := make(map[string]bool)
	for _, lig := range mapping[pdbID] {
		id := strings.ToUpper(strings.TrimSpace(lig.ChemCompID))
		if id == "" || crystallizationLigands[id] || seen[id] {
			continue
		}
		seen[id] = true
	}
	return len(seen)
}

// resolutionBetter reports whether resolution a is strictly better (finer) than b.
// Lower is better; a non-positive value means "unknown" (e.g. NMR entries report
// no resolution) and never beats a known resolution.
func resolutionBetter(a, b float64) bool {
	if a <= 0 {
		return false
	}
	if b <= 0 {
		return true
	}
	return a < b
}

// buildNotes assembles short human-readable strings explaining why a candidate was
// picked: holo/apo status, resolution, and the SIFTS coverage that resolves the residue.
func buildNotes(c siftsBestStructure, position, ligandCount int, holo bool) []string {
	notes := make([]string, 0, 3)
	if holo {
		notes = append(notes, fmt.Sprintf("holo: %d drug-like ligand(s)", ligandCount))
	} else {
		notes = append(notes, "apo: no drug-like ligands detected")
	}
	if c.Resolution > 0 {
		notes = append(notes, fmt.Sprintf("resolution %.2fÅ", c.Resolution))
	} else if c.ExperimentalMethod != "" {
		notes = append(notes, "resolution unavailable ("+c.ExperimentalMethod+")")
	}
	notes = append(notes, fmt.Sprintf("covers residue %d via SIFTS unp %d–%d", position, c.UnpStart, c.UnpEnd))
	return notes
}

// pdbeGET performs a single GET against the PDBe API and returns the (possibly
// gzip-decompressed) body. A non-200 status is an error so callers can treat a
// 404/empty entry as "skip this candidate". Gzip is detected by magic bytes so it
// works whether or not the transport transparently decompressed the stream.
func pdbeGET(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := pdbeClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pdbe GET failed for %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pdbe: status %d for %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pdbe: read failed for %s: %w", rawURL, err)
	}

	// Decompress if the body is gzip-encoded (magic bytes 0x1f 0x8b), mirroring
	// the defensive handling in uniprot.go / alphafold.go.
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("pdbe: gzip decompress failed: %w", err)
		}
		defer gr.Close()
		decompressed, err := io.ReadAll(gr)
		if err != nil {
			return nil, fmt.Errorf("pdbe: gzip read failed: %w", err)
		}
		body = decompressed
	}

	return body, nil
}
