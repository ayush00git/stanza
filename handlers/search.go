package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/ayush00git/stanza/models"
	"github.com/ayush00git/stanza/scoring"
	"github.com/ayush00git/stanza/services"
)

// searchResultLimit caps how many UniProt hits we hydrate per query. With the
// batched search call the metadata cost is a single request regardless of this
// number; the remaining per-protein AlphaFold lookups run concurrently and
// stream in as they resolve, so a higher cap no longer means a slower response.
const searchResultLimit = 100

// alphafoldConcurrency bounds simultaneous AlphaFold structure lookups per
// search so we stay a well-behaved client and don't trip its rate limiting.
const alphafoldConcurrency = 12

// SearchHandler streams search results via Server-Sent Events.
// Live UniProt results are streamed as each protein is enriched concurrently.
func SearchHandler(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' is required"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	ctx := c.Request.Context()

	// One batched call hydrates all hits (name, gene, organism, taxon, disease)
	// in a single request — no per-protein UniProt follow-ups.
	entries, err := services.SearchUniProtEntries(query, searchResultLimit)
	if err != nil {
		log.Printf("[search] uniprot search failed for %q after retries: %v", query, err)
	}
	if err != nil || len(entries) == 0 {
		sseDone(c.Writer, flusher, "fallback")
		return
	}

	seen := make(map[string]bool)

	resultCh := make(chan models.Complex, len(entries))
	var wg sync.WaitGroup

	// Bound concurrent AlphaFold lookups. Without this, a 100-result query fires
	// 100 simultaneous requests at the AlphaFold API and trips its rate limiting,
	// which can fail an entire search. The semaphore keeps at most
	// alphafoldConcurrency calls in flight; results still stream as they resolve.
	sem := make(chan struct{}, alphafoldConcurrency)

	for _, entry := range entries {
		if entry.PrimaryAccession == "" || seen[entry.PrimaryAccession] {
			continue
		}
		seen[entry.PrimaryAccession] = true
		wg.Add(1)
		go func(e *services.UniProtEntry) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}

			// Acquire a slot, respecting client disconnects while we wait.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			complex, err := buildComplexForSearch(e)
			if err != nil {
				return
			}
			resultCh <- *complex
		}(entry)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for result := range resultCh {
		if ctx.Err() != nil {
			return
		}
		sseResult(c.Writer, flusher, result)
	}

	sseDone(c.Writer, flusher, "live")
}

func sseResult(w http.ResponseWriter, f http.Flusher, c models.Complex) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: result\ndata: %s\n\n", data)
	f.Flush()
}

func sseDone(w http.ResponseWriter, f http.Flusher, source string) {
	fmt.Fprintf(w, "event: done\ndata: {\"source\":\"%s\"}\n\n", source)
	f.Flush()
}

func sseError(w http.ResponseWriter, f http.Flusher, msg string) {
	fmt.Fprintf(w, "event: error\ndata: {\"error\":\"%s\"}\n\n", msg)
	f.Flush()
}

// buildComplexForSearch builds a Complex from a pre-fetched UniProt entry,
// without fetching ChEMBL drug coverage. The entry already carries all metadata
// (from the batched search call), so the only network work here is the AlphaFold
// structure lookup. DrugCount is set to -1 (unknown); full drug data is fetched
// on demand by ComplexDetailHandler.
func buildComplexForSearch(uniEntry *services.UniProtEntry) (*models.Complex, error) {
	uniprotID := uniEntry.PrimaryAccession

	if !strings.Contains(uniEntry.EntryType, "Swiss-Prot") {
		return nil, fmt.Errorf("skipping unreviewed entry %s", uniprotID)
	}

	afData, err := services.FetchComplexData(uniprotID)
	if err != nil {
		return nil, err
	}

	isWHO := scoring.IsWHOPathogen(uniEntry.Organism.TaxonID, uniEntry.Organism.ScientificName)

	var diseases []string
	for _, comment := range uniEntry.Comments {
		if comment.CommentType == "DISEASE" && comment.Disease.DiseaseID != "" {
			diseases = append(diseases, comment.Disease.DiseaseID)
		}
	}

	geneName := ""
	if len(uniEntry.Genes) > 0 {
		geneName = uniEntry.Genes[0].GeneName.Value
	}

	return &models.Complex{
		UniprotID:        uniprotID,
		ProteinName:      uniEntry.ProteinDescription.RecommendedName.FullName.Value,
		GeneName:         geneName,
		Organism:         uniEntry.Organism.ScientificName,
		OrganismID:       uniEntry.Organism.TaxonID,
		IsWHOPathogen:    isWHO,
		DiseaseAssoc:     diseases,
		MonomerPLDDTAvg:  afData.MonomerPLDDT,
		DimerPLDDTAvg:    afData.DimerPLDDT,
		DisorderDelta:    afData.DisorderDelta,
		DrugCount:        -1,
		KnownDrugNames:   nil,
		MonomerStructURL: afData.MonomerCifURL,
		ComplexStructURL: afData.ComplexCifURL,
		Category:         inferCategory(isWHO, diseases, afData.DisorderDelta),
		AlphafoldID:      afData.MonomerEntryID,
		ReviewStatus:     "reviewed",
	}, nil
}

// buildComplexFromUniProt builds a Complex from a UniProt accession. The two
// upstream lookups it needs — UniProt metadata and the AlphaFold structure data
// — are independent, so they run concurrently and the function returns as soon
// as the slower of the two resolves. It deliberately does NOT fetch ChEMBL drug
// coverage (that lookup is slow and paginates every activity page for the
// target); DrugCount is set to -1 (unknown) and callers fetch drug data lazily
// via ComplexDrugsHandler. Shared by ComplexDetailHandler and the search path.
func buildComplexFromUniProt(uniprotID string) (*models.Complex, error) {
	var (
		uniEntry *services.UniProtEntry
		afData   *services.ComplexData
		uniErr   error
		afErr    error
		wg       sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		uniEntry, uniErr = services.FetchUniProtEntry(uniprotID)
	}()
	go func() {
		defer wg.Done()
		afData, afErr = services.FetchComplexData(uniprotID)
	}()
	wg.Wait()

	if uniErr != nil {
		return nil, uniErr
	}
	if !strings.Contains(uniEntry.EntryType, "Swiss-Prot") {
		return nil, fmt.Errorf("skipping unreviewed entry %s", uniprotID)
	}
	if afErr != nil {
		return nil, afErr
	}

	isWHO := scoring.IsWHOPathogen(uniEntry.Organism.TaxonID, uniEntry.Organism.ScientificName)

	var diseases []string
	for _, comment := range uniEntry.Comments {
		if comment.CommentType == "DISEASE" && comment.Disease.DiseaseID != "" {
			diseases = append(diseases, comment.Disease.DiseaseID)
		}
	}

	geneName := ""
	if len(uniEntry.Genes) > 0 {
		geneName = uniEntry.Genes[0].GeneName.Value
	}

	reviewStatus := "unreviewed"
	if strings.Contains(uniEntry.EntryType, "Swiss-Prot") {
		reviewStatus = "reviewed"
	}

	return &models.Complex{
		UniprotID:        uniprotID,
		ProteinName:      uniEntry.ProteinDescription.RecommendedName.FullName.Value,
		GeneName:         geneName,
		Organism:         uniEntry.Organism.ScientificName,
		OrganismID:       uniEntry.Organism.TaxonID,
		IsWHOPathogen:    isWHO,
		DiseaseAssoc:     diseases,
		MonomerPLDDTAvg:  afData.MonomerPLDDT,
		DimerPLDDTAvg:    afData.DimerPLDDT,
		DisorderDelta:    afData.DisorderDelta,
		DrugCount:        -1,
		KnownDrugNames:   nil,
		MonomerStructURL: afData.MonomerCifURL,
		ComplexStructURL: afData.ComplexCifURL,
		Category:         inferCategory(isWHO, diseases, afData.DisorderDelta),
		AlphafoldID:      afData.MonomerEntryID,
		ReviewStatus:     reviewStatus,
	}, nil
}

func inferCategory(isWHO bool, diseases []string, disorderDelta float64) string {
	if isWHO {
		return "who_pathogen"
	}
	if len(diseases) > 0 {
		return "human_disease"
	}
	if disorderDelta > 0.0 {
		return "high_disorder_delta"
	}
	return "monomer_only"
}
