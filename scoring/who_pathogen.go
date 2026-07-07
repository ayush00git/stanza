package scoring

import "strings"

// WHOPathogenOrganismIDs holds NCBI taxonomy IDs of pathogens on the WHO
// Bacterial Priority Pathogens List 2024 (BPPL 2024).
// Source: https://www.who.int/publications/i/item/9789240093461
//
// It contains species-level taxIDs plus the common reference-proteome strain
// taxIDs (which is what UniProt/AlphaFold usually annotate — e.g. a TB protein
// carries the H37Rv strain taxID, not the species). For any other strain or
// clinical isolate, IsWHOPathogen falls back to a species-name match, so the
// list below need not be exhaustive at the strain level.
var WHOPathogenOrganismIDs = map[int]bool{
	// CRITICAL PRIORITY
	470:    true, // Acinetobacter baumannii
	1773:   true, // Mycobacterium tuberculosis (species)
	83332:  true, // Mycobacterium tuberculosis H37Rv (reference strain)
	573:    true, // Klebsiella pneumoniae (Enterobacterales)
	562:    true, // Escherichia coli (Enterobacterales)
	83333:  true, // Escherichia coli K-12 (reference strain)
	550:    true, // Enterobacter cloacae (Enterobacterales)

	// HIGH PRIORITY
	1352:   true, // Enterococcus faecium
	287:    true, // Pseudomonas aeruginosa (species)
	208964: true, // Pseudomonas aeruginosa PAO1 (reference strain)
	1280:   true, // Staphylococcus aureus (species)
	93061:  true, // Staphylococcus aureus NCTC 8325 (reference strain)
	485:    true, // Neisseria gonorrhoeae (species)
	242231: true, // Neisseria gonorrhoeae FA 1090 (reference strain)
	90370:  true, // Salmonella enterica serovar Typhi (serovar)
	220341: true, // Salmonella enterica serovar Typhi CT18 (reference strain)
	28901:  true, // Salmonella enterica (non-typhoidal)
	99287:  true, // Salmonella enterica serovar Typhimurium LT2 (reference strain)
	620:    true, // Shigella (genus)

	// MEDIUM PRIORITY
	1314:   true, // Streptococcus pyogenes (Group A)
	1313:   true, // Streptococcus pneumoniae (species)
	170187: true, // Streptococcus pneumoniae TIGR4 (reference strain)
	727:    true, // Haemophilus influenzae
	1311:   true, // Streptococcus agalactiae (Group B)
}

// whoPathogenSpecies lists the species binomials (lower-case) on the WHO BPPL
// 2024, used for name-based matching. UniProt organism names are strain-
// qualified (e.g. "Mycobacterium tuberculosis (strain ATCC 25618 / H37Rv)"), so
// a substring match on the species catches every strain and clinical isolate —
// not just the taxIDs enumerated above.
var whoPathogenSpecies = []string{
	"acinetobacter baumannii",
	"klebsiella pneumoniae",
	"escherichia coli",
	"enterobacter cloacae",
	"mycobacterium tuberculosis",
	"pseudomonas aeruginosa",
	"enterococcus faecium",
	"staphylococcus aureus",
	"neisseria gonorrhoeae",
	"salmonella enterica",
	"shigella",
	"streptococcus pyogenes",
	"streptococcus pneumoniae",
	"streptococcus agalactiae",
	"haemophilus influenzae",
}

// IsWHOPathogen reports whether an organism is on the WHO priority pathogen list.
// It first checks the taxID (exact, fast) and otherwise matches the organism's
// scientific name against the WHO species binomials, which handles any strain.
func IsWHOPathogen(organismID int, organismName string) bool {
	if WHOPathogenOrganismIDs[organismID] {
		return true
	}
	name := strings.ToLower(organismName)
	for _, sp := range whoPathogenSpecies {
		if strings.Contains(name, sp) {
			return true
		}
	}
	return false
}
