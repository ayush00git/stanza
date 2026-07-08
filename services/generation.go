package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/ayush00git/stanza/models"
)

const (
	// genModel is the model that proposes molecules. Opus is used for the
	// hardest reasoning — designing chemistry that exploits a specific pocket.
	genModel     = anthropic.ModelClaudeOpus4_8
	genMaxTokens = 16000
	proposeTool  = "propose_molecules"
	// generation loop bounds (docking budget is the constraint).
	maxGenRounds  = 4
	maxGenPerRound = 8
	// historyForPrompt caps how many prior molecules are shown to the model.
	historyForPrompt = 12
)

// ProposeMolecules asks Claude for `n` novel drug-like SMILES that should bind the
// mutant resistance pocket while sparing the wild type, conditioned on the pocket
// context, the WT→mutant delta, and the prior rounds' selectivity scores. It uses a
// tool so the output is a clean SMILES list, not prose.
func ProposeMolecules(ctx context.Context, pctx *models.MutantPocketContext, mutation models.Mutation, history []models.LigandDock, n int) ([]string, error) {
	if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	client := anthropic.NewClient()

	system := "You are a medicinal chemist assisting an academic structure-based drug-discovery project. Suggest " +
		"candidate drug-like small-molecule inhibitors for a validated therapeutic protein target, given a description " +
		"of its binding pocket. The target has a clinically observed point mutation, and the aim is a mutant-selective " +
		"therapy — like the approved medicines osimertinib, sotorasib, and adagrasib — that is more active against the " +
		"mutant form of the target than the normal form, so it treats the disease while sparing healthy tissue. Docking " +
		"scores (kcal/mol, where more negative means tighter binding) are provided for previously suggested molecules; " +
		"a mutant-selective candidate binds the mutant pocket more tightly than the wild-type pocket. Suggest novel, " +
		"synthesizable, Lipinski-drug-like molecules, informed by the pocket's shape and chemistry. Return your " +
		"suggestions by calling the propose_molecules tool with SMILES."

	user := buildGenerationPrompt(pctx, mutation, history, n)

	tool := anthropic.ToolParam{
		Name:        proposeTool,
		Description: anthropic.String("Submit candidate molecules to evaluate, as valid SMILES strings."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"candidates": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description":  "Novel, drug-like, valid SMILES strings, distinct from any already tried.",
				},
			},
			Required: []string{"candidates"},
		},
	}

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     genModel,
		MaxTokens: genMaxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		// Adaptive thinking: let the model reason about the pocket before proposing.
		Thinking: anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
		Tools:    []anthropic.ToolUnionParam{{OfTool: &tool}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude request failed: %w", err)
	}

	for _, block := range resp.Content {
		if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok && tu.Name == proposeTool {
			var out struct {
				Candidates []string `json:"candidates"`
			}
			if err := json.Unmarshal([]byte(tu.JSON.Input.Raw()), &out); err != nil {
				return nil, fmt.Errorf("claude: could not parse candidates: %w", err)
			}
			return out.Candidates, nil
		}
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return nil, fmt.Errorf("claude declined the request (refusal category %q: %s)",
			resp.StopDetails.Category, resp.StopDetails.Explanation)
	}
	return nil, fmt.Errorf("claude proposed no molecules (stop reason %q)", resp.StopReason)
}

// buildGenerationPrompt renders the pocket context, the mutation delta, and the
// scored history into the per-round user message.
func buildGenerationPrompt(pctx *models.MutantPocketContext, mutation models.Mutation, history []models.LigandDock, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Resistance mutation: %s (wild-type %s at position %d → mutant %s).\n\n",
		mutation.Raw, mutation.WildType, mutation.Position, mutation.Mutant)

	mp := pctx.MutantPocket
	fmt.Fprintf(&b, "The mutant binding pocket to target:\n")
	fmt.Fprintf(&b, "- key residues: %s\n", strings.Join(mp.KeyResidues, ", "))
	fmt.Fprintf(&b, "- volume: %.0f Å³, hydrophobicity: %.1f\n", mp.Volume, mp.Hydrophobicity)

	d := pctx.PocketDelta
	fmt.Fprintf(&b, "\nWhat the mutation changed (WT → mutant):\n")
	if len(d.Changed) > 0 {
		fmt.Fprintf(&b, "- substitution: %s\n", strings.Join(d.Changed, ", "))
	}
	fmt.Fprintf(&b, "- Δvolume %.1f Å³, Δhydrophobicity %.1f, Δpolarity %.1f\n", d.DVolume, d.DHydrophobicity, d.DPolarity)
	if len(d.ResiduesGained) > 0 {
		fmt.Fprintf(&b, "- residues gained in the mutant pocket: %s\n", strings.Join(d.ResiduesGained, ", "))
	}
	if len(d.ResiduesLost) > 0 {
		fmt.Fprintf(&b, "- residues lost from the pocket: %s\n", strings.Join(d.ResiduesLost, ", "))
	}
	if d.Effect != "" {
		fmt.Fprintf(&b, "- effect: %s\n", d.Effect)
	}

	if len(history) > 0 {
		ranked := append([]models.LigandDock(nil), history...)
		sort.Slice(ranked, func(i, j int) bool { return ranked[i].Selectivity > ranked[j].Selectivity })
		if len(ranked) > historyForPrompt {
			ranked = ranked[:historyForPrompt]
		}
		fmt.Fprintf(&b, "\nMolecules already evaluated (wt_score / mutant_score / selectivity, kcal/mol — higher selectivity is better; do NOT repeat these):\n")
		for _, h := range ranked {
			fmt.Fprintf(&b, "- %s  |  wt %.2f  mut %.2f  sel %+.2f\n", h.SMILES, h.WTScore, h.MutantScore, h.Selectivity)
		}
		b.WriteString("\nUse the pattern to guide new suggestions: which scaffolds and substitutions improved the mutant-vs-wild-type preference, and which did not.\n")
	}

	fmt.Fprintf(&b, "\nSuggest %d NEW candidate molecules (SMILES) that are drug-like and likely to be more active "+
		"against the mutant form than the wild-type form. Prioritize novelty and diversity over the molecules already tried.\n", n)
	return b.String()
}

// RunGenerationLoop is Stage 6. It runs the Claude-orchestrated generate → dock →
// score → feed-back loop for a run: each round Claude proposes molecules, each new
// one is docked into both tracks (reusing the dual-track dock + its per-SMILES
// cache), the results feed the next round, and the run's dock leaderboard is left
// sorted by selectivity. Requires the run's structures (Stage 2); it runs Stage-3
// pocket analysis first if needed.
func RunGenerationLoop(ctx context.Context, run *models.Run, rounds, n int) error {
	if rounds <= 0 {
		rounds = 2
	}
	if rounds > maxGenRounds {
		rounds = maxGenRounds
	}
	if n <= 0 {
		n = 4
	}
	if n > maxGenPerRound {
		n = maxGenPerRound
	}

	if run.Mutagenesis == nil {
		return fmt.Errorf("generation: run has no structures (run Stage-2 mutagenesis first)")
	}
	if run.Pockets == nil || run.Pockets.Context == nil {
		pa, err := BuildPocketAnalysis(ctx, run)
		if err != nil {
			return fmt.Errorf("generation: pocket analysis: %w", err)
		}
		run.Pockets = pa
	}
	if run.Pockets.Context == nil {
		return fmt.Errorf("generation: no resistance pocket to design against")
	}

	seen := make(map[string]bool)
	for _, d := range run.Docks {
		seen[d.SMILES] = true
	}

	for r := 0; r < rounds; r++ {
		candidates, err := ProposeMolecules(ctx, run.Pockets.Context, run.Mutation, run.Docks, n)
		if err != nil {
			return fmt.Errorf("generation round %d: %w", r+1, err)
		}
		for _, smi := range candidates {
			smi = strings.TrimSpace(smi)
			if smi == "" || seen[smi] {
				continue
			}
			seen[smi] = true
			dock, derr := DockLigandDualTrack(ctx, run, smi)
			if derr != nil {
				// Invalid SMILES or a failed dock: skip the molecule, keep the loop going.
				continue
			}
			run.Docks = append(run.Docks, *dock)
		}
	}

	// Leave the leaderboard ranked by selectivity (most mutant-selective first).
	sort.Slice(run.Docks, func(i, j int) bool { return run.Docks[i].Selectivity > run.Docks[j].Selectivity })
	return nil
}
