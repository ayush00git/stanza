package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/ayush00git/stanza/models"
)

const (
	// genModel is the model that proposes molecules. Opus is used for the
	// hardest reasoning — designing chemistry aimed at a specific pocket.
	genModel     = anthropic.ModelClaudeOpus4_8
	genMaxTokens = 16000
	proposeTool  = "propose_molecules"
	// maxGenPerCall caps how many molecules one generate call requests, so a
	// single call's cost — and the list handed back to the UI — stays bounded.
	maxGenPerCall = 8
	// historyForPrompt caps how many prior molecules are shown to the model.
	historyForPrompt = 12
)

// ProposeMolecules asks Claude for `n` novel drug-like SMILES that should bind the
// mutant resistance pocket while sparing the wild type, conditioned on the pocket
// context, the WT→mutant delta, and the selectivity scores of any molecules already
// docked for this run. It uses a tool so the output is a clean SMILES list, not prose.
func ProposeMolecules(ctx context.Context, pctx *models.MutantPocketContext, mutation models.Mutation, site *KnownSite, covalentResidue string, history []models.LigandDock, n int) ([]string, error) {
	if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	client := anthropic.NewClient()

	system := "You are a medicinal chemist assisting an academic structure-based drug-discovery project. Suggest " +
		"candidate drug-like small-molecule inhibitors for a validated therapeutic protein target, given a description " +
		"of its binding pocket. The target has a clinically observed point mutation, and the aim is a mutant-selective " +
		"therapy — like the approved medicines osimertinib, sotorasib, and adagrasib — that acts on the mutant form of " +
		"the target while sparing the normal form, so it treats the disease without harming healthy tissue. Suggest " +
		"novel, synthesizable, Lipinski-drug-like molecules, informed by the pocket's shape and chemistry, and return " +
		"them by calling the propose_molecules tool with SMILES.\n\n" +
		"Docking scores (kcal/mol, more negative = tighter) are provided for molecules already evaluated. Read them " +
		"carefully but do not assume tighter binding means selective. Selectivity does not always come from binding " +
		"the mutant pocket more tightly: where a mutation introduces a reactive residue, a covalent bond the wild type " +
		"cannot form is the entire mechanism, and the reversible docking scores of the two forms will be nearly " +
		"identical. The prompt states which regime this target is in; optimise the quantity it tells you to."

	if covalentResidue != "" {
		system += "\n\nFor a covalent target: a warhead that cannot reach and attack the reactive residue is worthless " +
			"regardless of how well the molecule docks, and a molecule that reaches it but occupies only a fraction of " +
			"the pocket will be far too weak to matter. Both must hold."
	}

	user := buildGenerationPrompt(pctx, mutation, site, covalentResidue, history, n)

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
func buildGenerationPrompt(pctx *models.MutantPocketContext, mutation models.Mutation, site *KnownSite, covalentResidue string, history []models.LigandDock, n int) string {
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

	if covalentResidue != "" {
		writeCovalentBrief(&b, covalentResidue, site)
	}

	if len(history) > 0 {
		writeHistory(&b, history, covalentResidue != "")
	}

	fmt.Fprintf(&b, "\nSuggest %d NEW candidate molecules (SMILES). Prioritise novelty and diversity over the "+
		"molecules already tried.\n", n)
	return b.String()
}

// writeCovalentBrief states the mechanism, the constraints, and the prior art. Left
// unsaid, the model reaches for what it remembers: asked for KRAS G12C binders it
// returned truncated ARS-1620 analogues, below the viable weight range, missing the
// substituent that makes the series potent.
func writeCovalentBrief(b *strings.Builder, covalentResidue string, site *KnownSite) {
	fmt.Fprintf(b, "\nThis is a COVALENT target. The mutation installs %s, and its thiol is the anchor.\n",
		covalentResidue)

	if site != nil && site.Guidance != nil {
		g := site.Guidance
		fmt.Fprintf(b, "\nWhere selectivity comes from:\n%s\n", g.Mechanism)
		fmt.Fprintf(b, "\nWhat a potent ligand must carry here:\n- %s\n", g.Pharmacophore)
		if g.MinMW > 0 && g.MaxMW > 0 {
			fmt.Fprintf(b, "- molecular weight in the %.0f–%.0f Da range; smaller fragments reach the thiol "+
				"but bind too weakly to be useful\n", g.MinMW, g.MaxMW)
		}
		if len(g.PriorArt) > 0 {
			fmt.Fprintf(b, "\nAlready published — propose genuinely new scaffolds, not analogues of these:\n")
			for _, p := range g.PriorArt {
				fmt.Fprintf(b, "- %s\n", p)
			}
		}
	}

	fmt.Fprintf(b, "\nEvery candidate must carry a cysteine-reactive warhead (acrylamide, substituted acrylamide, "+
		"cyanoacrylamide, vinyl sulfonamide, propiolamide or haloacetamide) positioned so its electrophilic carbon "+
		"can reach %s and attack it along a viable trajectory — roughly 105° onto a Michael acceptor's β-carbon, "+
		"or collinear for SN2. Vary the warhead class; do not send six acrylamides.\n", covalentResidue)
}

// writeHistory shows what was measured, ordered by the quantity worth optimising.
//
// For a covalent target that quantity is feasibility, not selectivity. Ranking the
// history by selectivity and calling the top entries the winners — as this prompt used
// to — feeds the model a zero-mean noise signal every round and asks it to chase it:
// Gly12→Cys12 barely perturbs the reversible contact set, so the WT and mutant docking
// scores agree to ~0.1 kcal/mol and their difference is sampling error.
func writeHistory(b *strings.Builder, history []models.LigandDock, covalent bool) {
	ranked := append([]models.LigandDock(nil), history...)
	if covalent {
		sort.SliceStable(ranked, func(i, j int) bool {
			return covalentFeasibility(ranked[i]) > covalentFeasibility(ranked[j])
		})
	} else {
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Selectivity > ranked[j].Selectivity })
	}
	if len(ranked) > historyForPrompt {
		ranked = ranked[:historyForPrompt]
	}

	if !covalent {
		b.WriteString("\nMolecules already evaluated (wt_score / mutant_score / selectivity, kcal/mol — higher " +
			"selectivity is better; do NOT repeat these):\n")
		for _, h := range ranked {
			fmt.Fprintf(b, "- %s  |  wt %.2f  mut %.2f  sel %+.2f\n", h.SMILES, h.WTScore, h.MutantScore, h.Selectivity)
		}
		b.WriteString("\nUse the pattern to guide new suggestions: which scaffolds and substitutions improved the " +
			"mutant-vs-wild-type preference, and which did not.\n")
		return
	}

	b.WriteString("\nMolecules already evaluated, best first. `feas` is covalent feasibility in [0,1]: can the " +
		"warhead reach the thiol and attack it? It is the quantity to optimise. `mut` is the raw mutant docking " +
		"affinity (kcal/mol, more negative = tighter) and still matters — a feasible warhead on a weak binder is " +
		"useless. Do NOT repeat these:\n")
	for _, h := range ranked {
		fmt.Fprintf(b, "- %s  |  mut %.2f  ", h.SMILES, h.MutantScore)
		switch {
		case h.Covalent == nil:
			b.WriteString("feas —  (no warhead detected)")
		case h.Covalent.Uncertain:
			fmt.Fprintf(b, "feas %.2f  (UNRELIABLE: the covalent call flips with the docking seed)", h.Covalent.Feasibility)
		case h.Covalent.Feasibility <= 0:
			fmt.Fprintf(b, "feas 0.00  (warhead cannot attack: reach %.2f Å, attack angle %.0f°)",
				h.Covalent.ReachDistance, h.Covalent.AttackAngle)
		default:
			fmt.Fprintf(b, "feas %.2f  (reach %.2f Å, attack angle %.0f°)",
				h.Covalent.Feasibility, h.Covalent.ReachDistance, h.Covalent.AttackAngle)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nThe wild-type and mutant docking scores are omitted on purpose: they agree to within noise, " +
		"because the two pockets are nearly identical without the covalent bond. Do not try to widen that gap. " +
		"Improve the warhead's geometry and the scaffold's grip on the pocket.\n")
}

// covalentFeasibility ranks a dock for the prompt. A molecule whose covalent call flips
// with the docking seed is not evidence of anything, so it sorts with the failures
// rather than on its median — the same rule the fitness function applies.
func covalentFeasibility(d models.LigandDock) float64 {
	if d.Covalent == nil || d.Covalent.Uncertain {
		return -1
	}
	return d.Covalent.Feasibility
}

// GenerateCandidates is Stage 6. It asks Claude for up to n drug-like molecules
// aimed at the run's mutant resistance pocket, runs the proposals through the
// Stage-5 RDKit pre-filter, and returns the validated survivors as scored
// Candidates WITHOUT docking. Docking is the slow step, so it is deliberately
// deferred: the caller docks a molecule on demand via DockLigandDualTrack (Stage 4)
// when the user picks one, the same list-then-dock flow used for ChEMBL fragments.
//
// Molecules already docked for this run are passed to Claude as scored history, so
// calling this again after some docks acts as an informal, user-driven selectivity
// feedback loop. Requires the run's structures (Stage 2); it runs Stage-3 pocket
// analysis first if needed. The RDKit filter drops invalid, duplicate (run-scoped
// by InChIKey), and non-drug-like proposals; the kept ones are merged into
// run.Candidates and returned. mu guards every read/write of the run's mutable
// fields so it is safe under concurrent handlers.
func GenerateCandidates(ctx context.Context, run *models.Run, n int, mu *sync.Mutex) ([]models.Candidate, error) {
	if n <= 0 {
		n = 4
	}
	if n > maxGenPerCall {
		n = maxGenPerCall
	}

	if run.Mutagenesis == nil {
		return nil, fmt.Errorf("generation: run has no structures (run Stage-2 mutagenesis first)")
	}

	// Ensure Stage-3 pocket analysis has run — the proposal is conditioned on the
	// mutant pocket context and the WT→mutant delta.
	mu.Lock()
	ready := run.Pockets != nil && run.Pockets.Context != nil
	mu.Unlock()
	if !ready {
		pa, err := BuildPocketAnalysis(ctx, run)
		if err != nil {
			return nil, fmt.Errorf("generation: pocket analysis: %w", err)
		}
		mu.Lock()
		run.Pockets = pa
		mu.Unlock()
	}

	// Snapshot the pocket context, the scored history, and the identities already
	// known for the run (so the RDKit filter dedupes across calls, not just batches).
	mu.Lock()
	pctx := run.Pockets.Context
	history := append([]models.LigandDock(nil), run.Docks...)
	seenKeys := make([]string, 0, len(run.Candidates))
	for _, c := range run.Candidates {
		if c.InChIKey != "" {
			seenKeys = append(seenKeys, c.InChIKey)
		}
	}
	mu.Unlock()
	if pctx == nil {
		return nil, fmt.Errorf("generation: no resistance pocket to design against")
	}

	// A mutation that installs a cysteine changes what the generator must optimise: the
	// covalent bond, not the docking margin. The curated site supplies the rest — the
	// pharmacophore, the viable weight range, and the prior art to avoid re-deriving.
	var covalentResidue string
	if isCovalentTarget(run.Mutagenesis.MutantResidue) {
		covalentResidue = resToken(run.Mutagenesis.MutantResidue, run.Mutagenesis.TargetResidueNum)
	}
	site := LookupKnownSite(run.UniprotID, run.Mutation, run.SiteHint)

	proposed, err := ProposeMolecules(ctx, pctx, run.Mutation, site, covalentResidue, history, n)
	if err != nil {
		return nil, err
	}

	// Stage 5: RDKit pre-filter — parse, canonicalize, dedupe, and drug-likeness
	// gate, so the dock budget is spent only on viable, unique molecules.
	verdicts, err := ValidateSMILES(ctx, run.ID, proposed, seenKeys)
	if err != nil {
		return nil, fmt.Errorf("generation: validation: %w", err)
	}

	var fresh []models.Candidate
	dropped := make(map[string]int)
	for _, v := range verdicts {
		if v.Kept {
			fresh = append(fresh, verdictToCandidate(v))
		} else {
			dropped[v.DropReason]++
		}
	}
	log.Printf("[gen:%s] validation: %d proposed, %d kept, dropped %v", shortID(run.ID), len(verdicts), len(fresh), dropped)

	mu.Lock()
	run.Candidates = append(run.Candidates, fresh...)
	mu.Unlock()

	return fresh, nil
}

// verdictToCandidate projects a kept RDKit verdict onto the stored Candidate shape.
func verdictToCandidate(v MoleculeVerdict) models.Candidate {
	c := models.Candidate{SMILES: v.SMILES, InChIKey: v.InChIKey, SAScore: v.SAScore}
	if v.QED != nil {
		c.QED = *v.QED
	}
	if v.RO5Pass != nil {
		c.RO5Pass = *v.RO5Pass
	}
	if v.MolWeight != nil {
		c.MolWeight = *v.MolWeight
	}
	if v.LogP != nil {
		c.LogP = *v.LogP
	}
	return c
}

// shortID returns a log-friendly prefix of a run ID.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
