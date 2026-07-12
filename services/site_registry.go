package services

import (
	"strings"
	"sync"

	"github.com/ayush00git/stanza/models"
)

// The site registry is a runtime, in-memory layer over the hand-curated registry
// in known_sites.go. Hardcoded curation is what Stanza ships: a small set of sites
// its authors have read the literature for and encoded as KnownSite values. That set
// can never cover every target a user might bring, and waiting for a code change to
// add one defeats the paper-extraction flow entirely.
//
// So a confirmed models.ExtractedSite — a site a person has ratified after Claude
// pulled it from a paper — is converted to a KnownSite and stored here at runtime.
// LookupKnownSite consults this registry FIRST, before the hardcoded map, so a user
// can design against a target Stanza ships no curation for, and a registered paper
// site takes precedence for its exact uniprot+mutation. Entries live only for the
// process's lifetime; nothing here is persisted, and the hardcoded curation is the
// durable source of truth.
var (
	registryMu      sync.RWMutex
	registeredSites = map[string]*KnownSite{}
)

// registryKey is the registry's lookup key: uppercased "UNIPROTID/MUTATION",
// e.g. "P00533/C797S". A missing half yields "P00533/" or "/C797S"; the halves are
// compared verbatim so registration and lookup must agree on both.
func registryKey(uniprotID, mutation string) string {
	return strings.ToUpper(strings.TrimSpace(uniprotID)) + "/" +
		strings.ToUpper(strings.TrimSpace(mutation))
}

// RegisterExtractedSite converts a confirmed models.ExtractedSite into a
// services.KnownSite and stores it in the runtime registry, keyed by its uppercased
// "UNIPROTID/MUTATION". A repeat registration for the same key overwrites the prior
// entry. This is the runtime, in-memory counterpart to the hardcoded knownSites map:
// confirmed paper extractions land here, the curated set lives in known_sites.go, and
// LookupKnownSite consults this registry first.
func RegisterExtractedSite(site *models.ExtractedSite) {
	if site == nil {
		return
	}

	ks := &KnownSite{
		Name:      site.ProteinName + " (from paper)",
		UniprotID: site.UniprotID,
		Residues:  site.PocketResidues,
	}
	if site.Mutation != "" {
		ks.Mutations = []string{site.Mutation}
	}

	// A named holo structure builds the WT/mutant pair on the real backbone; with no
	// PDB the template stays nil and the pipeline falls back to the AlphaFold model.
	if site.PDBID != "" {
		ks.Template = &SiteTemplate{
			PDBID: site.PDBID,
			Chain: site.Chain,
		}
	}

	// A covalent site whose reactive residue is NOT the mutation site (the case the
	// extraction works hardest to get right — EGFR C797S removes Cys797, so the design
	// bonds Cys775) carries that residue forward. Without it the generator would derive
	// the covalent anchor from the mutant residue (Ser797), decide the target is not
	// covalent, and design reversible molecules. Only set when the paper says covalent AND
	// names the residue, so a non-covalent site never triggers the warhead brief.
	if site.Covalent && strings.TrimSpace(site.ReactiveResidue) != "" {
		ks.CovalentResidue = strings.TrimSpace(site.ReactiveResidue)
	}

	// MassAnchors are left nil: the v1 paper flow does not extract per-compound masses.
	ks.Guidance = &SiteGuidance{
		Mechanism:     site.Mechanism,
		Pharmacophore: site.Pharmacophore,
		MinMW:         site.MinMW,
		MaxMW:         site.MaxMW,
		PriorArt:      site.PriorArt,
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	registeredSites[registryKey(site.UniprotID, site.Mutation)] = ks
}

// lookupRegisteredSite returns the registered paper site for this uniprot+mutation,
// or nil if none is registered. The lookup is read-locked so it is safe to call
// concurrently with registration.
func lookupRegisteredSite(uniprotID string, mut models.Mutation) *KnownSite {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registeredSites[registryKey(uniprotID, mut.String())]
}
