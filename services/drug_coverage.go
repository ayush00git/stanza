package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const chemblDataBase = "https://www.ebi.ac.uk/chembl/api/data"

var chemblDrugClient = &http.Client{Timeout: 10 * time.Second}

type chemblTargetList struct {
	Targets []struct {
		TargetChemblID string `json:"target_chembl_id"`
	} `json:"targets"`
}

type chemblActivityList struct {
	Activities []struct {
		MoleculePrefName string `json:"molecule_pref_name"`
	} `json:"activities"`
	PageMeta struct {
		TotalCount int `json:"total_count"`
	} `json:"page_meta"`
}

// FetchDrugCoverage queries ChEMBL for approved drugs (molecule max clinical phase = 4) for a UniProt accession.
// Returns (activity row count as proxy for drug coverage, distinct pref_names up to 5, error).
// On transport/parse failure returns (-1, nil, nil) to match historical callers.
func FetchDrugCoverage(uniprotID string) (int, []string, error) {
	uniprotID = strings.TrimSpace(uniprotID)
	if uniprotID == "" {
		return 0, []string{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tu := fmt.Sprintf("%s/target.json?limit=1&target_components__accession=%s", chemblDataBase, url.QueryEscape(uniprotID))
	tbody, err := chemblGET(ctx, tu)
	if err != nil {
		return -1, []string{}, nil
	}
	var tlist chemblTargetList
	if err := json.Unmarshal(tbody, &tlist); err != nil || len(tlist.Targets) == 0 {
		return 0, []string{}, nil
	}
	tid := tlist.Targets[0].TargetChemblID
	if tid == "" {
		return 0, []string{}, nil
	}

	// Paginate through activities to collect distinct approved drug names.
	// We only need unique molecule names, so limit=500 per page is efficient.
	seen := make(map[string]bool)
	var names []string
	offset := 0
	const pageSize = 500

	for {
		au := fmt.Sprintf("%s/activity.json?target_chembl_id=%s&molecule_max_phase=4&limit=%d&offset=%d",
			chemblDataBase, url.QueryEscape(tid), pageSize, offset)
		abody, err := chemblGET(ctx, au)
		if err != nil {
			break
		}
		var alist chemblActivityList
		if err := json.Unmarshal(abody, &alist); err != nil {
			break
		}
		if len(alist.Activities) == 0 {
			break
		}

		for _, a := range alist.Activities {
			pn := strings.TrimSpace(a.MoleculePrefName)
			if pn == "" || seen[pn] {
				continue
			}
			seen[pn] = true
			names = append(names, pn)
		}

		// Stop if we've fetched all rows or this was the last page
		offset += len(alist.Activities)
		if offset >= alist.PageMeta.TotalCount || len(alist.Activities) < pageSize {
			break
		}
	}

	// Drug count = number of distinct approved drugs, not activity rows
	drugCount := len(seen)
	// Keep only first 5 names for display
	if len(names) > 5 {
		names = names[:5]
	}

	return drugCount, names, nil
}

func chemblGET(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := chemblDrugClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chembl HTTP %d", resp.StatusCode)
	}
	return body, nil
}
