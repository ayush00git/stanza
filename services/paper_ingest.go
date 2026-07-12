package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/ayush00git/stanza/models"
)

const (
	// ingestModel reads the paper. Opus is used because the task is close reading —
	// distinguishing the mutation site from the reactive residue, and grounding every
	// field in the exact sentence it came from — not pattern matching.
	ingestModel     = anthropic.ModelClaudeOpus4_8
	ingestMaxTokens = 8000
	// extractTool is the single, forced tool. Forcing it makes Claude answer with a
	// structured ExtractedSite instead of prose we would have to re-parse.
	extractTool = "extract_site"
)

// ExtractSiteFromPDF asks Claude to read a scientific-paper PDF and pull out the
// curated-site draft the rest of the pipeline is built on — target identity, the
// reactive residue a covalent warhead should bond, the weight window, the prior art,
// and a holo PDB — with the exact source sentence beside every field it fills.
//
// It hands Claude the PDF as a document block and forces a tool call whose schema
// mirrors models.ExtractedSite, so the output is a parseable object rather than text.
// Nothing here is trusted blindly: the provenance in Citations is what lets a human
// ratify the draft before it drives docking, generation, and the weight gate.
func ExtractSiteFromPDF(ctx context.Context, pdf []byte, filename string) (*models.ExtractedSite, error) {
	if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	client := anthropic.NewClient()

	// The PDF ships as a base64 document block with no newlines in the payload.
	b64 := base64.StdEncoding.EncodeToString(pdf)

	system := "You are a medicinal-chemistry literature analyst. Your job is to read a paper and extract the " +
		"design parameters for a covalent/steric drug-design pipeline into the extract_site tool.\n\n" +
		"Read carefully, because the pipeline runs confidently on whatever you return:\n" +
		"- The reactive residue is NOT always the mutation site. A covalent warhead bonds a specific residue, " +
		"and a resistance mutation can remove the very residue the prior drug targeted. EGFR C797S destroys the " +
		"cysteine osimertinib bonds, so third-generation designs instead target Cys775 — reactive_residue would " +
		"be \"Cys775\" even though the mutation is C797S. Report the residue the paper says a warhead should attack, " +
		"not the mutation by reflex.\n" +
		"- For EVERY field you fill, put the exact verbatim sentence from the paper into the citations object, " +
		"keyed by that field's JSON name (e.g. \"reactive_residue\", \"min_mw\", \"pdb_id\"). The sentence must be " +
		"copied word for word from the paper, not paraphrased.\n" +
		"- If the paper does not state a field, leave it empty and do NOT invent a citation for it. A field you " +
		"cannot ground in the text is simply left out of citations rather than cited to nothing.\n" +
		"- Prefer a holo (ligand-bound) PDB that the paper names for pdb_id.\n\n" +
		"Honesty is the entire point. A fabricated number here — a weight window, a residue, a PDB — would drive " +
		"the whole pipeline wrong, and a person will check every field against the sentence you cite. Ground the " +
		"draft in the paper or leave it blank."

	tool := anthropic.ToolParam{
		Name:        extractTool,
		Description: anthropic.String("Submit the curated-site draft extracted from the paper, with a verbatim source sentence for every field filled."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"uniprot_id": map[string]any{
					"type":        "string",
					"description": "UniProt accession of the target, e.g. \"P00533\" (EGFR). Empty if the paper names none.",
				},
				"protein_name": map[string]any{
					"type":        "string",
					"description": "Full protein name, e.g. \"Epidermal growth factor receptor\".",
				},
				"mutation": map[string]any{
					"type":        "string",
					"description": "The point mutation in one-letter form, e.g. \"C797S\". Empty if the target is a wild-type residue.",
				},
				"reactive_residue": map[string]any{
					"type": "string",
					"description": "The residue a covalent warhead should bond, e.g. \"Cys775\". MAY DIFFER from the mutation " +
						"site: a mutation can remove the residue an earlier drug targeted, so the design bonds a different one. " +
						"Empty for a non-covalent target.",
				},
				"covalent": map[string]any{
					"type":        "boolean",
					"description": "True if the paper describes a covalent mechanism (a warhead forming a bond to a residue).",
				},
				"mechanism": map[string]any{
					"type":        "string",
					"description": "One paragraph on where selectivity actually comes from.",
				},
				"pharmacophore": map[string]any{
					"type":        "string",
					"description": "The substructure that drives potency at this site.",
				},
				"min_mw": map[string]any{
					"type":        "number",
					"description": "Molecular-weight window floor in Da, if the paper states one. 0 if not stated.",
				},
				"max_mw": map[string]any{
					"type":        "number",
					"description": "Molecular-weight window ceiling in Da, if the paper states one. 0 if not stated.",
				},
				"prior_art": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Published inhibitors named in the paper that a generator should not re-derive.",
				},
				"pocket_residues": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "integer"},
					"description": "UniProt-numbered residues the paper says line the pocket.",
				},
				"pdb_id": map[string]any{
					"type":        "string",
					"description": "A holo PDB the paper names, e.g. \"6OIM\". Empty if it names no usable structure.",
				},
				"chain": map[string]any{
					"type":        "string",
					"description": "Chain identifier for the PDB, e.g. \"A\".",
				},
				"citations": map[string]any{
					"type": "object",
					"description": "One verbatim source sentence per field above, keyed by the field's JSON name " +
						"(e.g. \"reactive_residue\", \"min_mw\", \"pdb_id\"). Omit a field entirely rather than citing it to " +
						"a sentence that does not state it.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"notes": map[string]any{
					"type":        "string",
					"description": "Your own flags: which fields you were unsure of, what the paper did not state, what the human should double-check.",
				},
			},
			Required: []string{"protein_name", "covalent", "citations"},
		},
	}

	user := fmt.Sprintf("Extract the curated-site draft from the attached paper (%s) by calling the extract_site "+
		"tool. Fill only the fields the paper supports, and cite each one verbatim.", filename)

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     ingestModel,
		MaxTokens: ingestMaxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		// Adaptive thinking: let the model reason about the paper — separating the mutation
		// site from the reactive residue — before committing to the extraction.
		Thinking: anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
		Tools:    []anthropic.ToolUnionParam{{OfTool: &tool}},
		// Force the one tool so the answer is a structured ExtractedSite, not prose.
		ToolChoice: anthropic.ToolChoiceUnionParam{OfTool: &anthropic.ToolChoiceToolParam{Name: extractTool}},
		Messages: []anthropic.MessageParam{
			// Document block BEFORE the text block in the user message.
			anthropic.NewUserMessage(
				anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{Data: b64}),
				anthropic.NewTextBlock(user),
			),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude request failed: %w", err)
	}

	for _, block := range resp.Content {
		if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok && tu.Name == extractTool {
			return parseExtraction([]byte(tu.JSON.Input.Raw()))
		}
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return nil, fmt.Errorf("claude declined the request (refusal category %q: %s)",
			resp.StopDetails.Category, resp.StopDetails.Explanation)
	}
	return nil, fmt.Errorf("claude returned no extraction (stop reason %q)", resp.StopReason)
}

// parseExtraction unmarshals the tool call's JSON input into an ExtractedSite. It is
// split out from ExtractSiteFromPDF so the parse can be exercised on a fixed blob
// without a live API call — the network is the only part of extraction a test cannot own.
func parseExtraction(raw []byte) (*models.ExtractedSite, error) {
	var site models.ExtractedSite
	if err := json.Unmarshal(raw, &site); err != nil {
		return nil, fmt.Errorf("could not parse extracted site: %w", err)
	}
	return &site, nil
}
