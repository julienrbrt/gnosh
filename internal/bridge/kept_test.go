package bridge

import (
	"encoding/json"
	"strings"
	"testing"
)

// dnaTask is the dna-assembly task of the round-2 benchmark, as the person
// typed it. Its last bullet is the one every extension trial left unchecked:
// the model's fetch of NEB's page returned a security check, and the pops
// that followed called the design validated.
const dnaTask = `The file titled sequences.fasta contains the following sequences:
  * input: A circular input plasmid.
  * egfp: A linear DNA sequence encoding the egfp protein.
Currently I have the input, egfp, flag, and snap sequences on hand and I want to combine them to make the output plasmid. I'll be using the NEBridge Golden Gate assembly kit with BsaI-HF v2 enzyme to assemble all the fragments together. However, I don't have enzyme cut-sites in my sequences so I'll need to PCR amplify them first.

Design some primers that will make my sequences ready for a one-pot golden gate assembly. The primers should also respect the following rules:
 * The part of the primers annealed to the template sequence should have a length between 15 and 45 nucleotides.
 * Have a melting temperature between 58 and 72 degrees celsius.
 * Output the minimum number of primer pairs necessary to complete this task.
 * The output fasta file should be titled primers.fasta.
 * If you aren't familiar with BsaI-HF v2 make sure to check that the enzyme cut-sites you design satisfy NEB's requirements.
 * The fasta file you create should not have any blank lines.
`

func TestRequirementLinesKeepsTheUncheckedRequirement(t *testing.T) {
	lines := requirementLines(dnaTask)
	if len(lines) == 0 {
		t.Fatal("no requirement lines")
	}
	// A check line comes first, word for word, so a cap never drops it.
	if !strings.Contains(lines[0], "satisfy NEB's requirements") {
		t.Errorf("the check line should come first, got %q", lines[0])
	}
	list := keptList(dnaTask, nil)
	for _, want := range []string{"(still to confirm) If you aren't familiar with BsaI-HF v2 make sure to check that the enzyme cut-sites you design satisfy NEB's requirements.", "between 15 and 45 nucleotides", "primers.fasta", "no blank lines"} {
		if want == "no blank lines" {
			want = "should not have any blank lines"
		}
		if !strings.Contains(list, want) {
			t.Errorf("kept list lacks %q:\n%s", want, list)
		}
	}
	if len(list) > keptMaxChars+400 {
		t.Errorf("kept list is %d bytes, want it short", len(list))
	}
}

func TestKeptListIsEmptyForATaskWithNothingToKeep(t *testing.T) {
	if got := keptList("Please tidy up the code a bit.", nil); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestUnsureOf(t *testing.T) {
	got := unsureOf("Primers designed and validated. The NEB page could not be fetched (403). Tm checked with oligotm; overhang spacing assumed 1 nt.")
	if len(got) != 2 || !strings.Contains(got[0], "could not be fetched") || !strings.Contains(got[1], "assumed") {
		t.Errorf("got %q", got)
	}
}

// §10.6: with keep, the session's first push is told what a pop's result
// must carry; later pushes, and a host that did not ask, are not.
func TestKeepInstructionOnFirstPushOnly(t *testing.T) {
	push := func(id string) string {
		return `{"op":"call","session":"S","call_id":"` + id + `","name":"t","input":{"frames":[{"kind":"push","id":"` + id + `","session":"S"}]}}`
	}
	s := withTexts(t)
	reps, err := serve(t, s, hello, `{"op":"prompt","session":"S","text":"go","keep":true,"lean":true}`, push("a"), push("b"))
	if err != nil {
		t.Fatal(err)
	}
	text := func(r Reply) string {
		var b strings.Builder
		for _, c := range r.Content {
			b.WriteString(c.Text)
		}
		return b.String()
	}
	if !strings.Contains(text(reps[2]), "word for word") || strings.Contains(text(reps[3]), "word for word") {
		t.Errorf("first push %q, second %q", text(reps[2]), text(reps[3]))
	}
	reps, _ = serve(t, withTexts(t), hello, push("a"))
	if strings.Contains(text(reps[1]), "word for word") {
		t.Errorf("without keep the push is as it was: %q", text(reps[1]))
	}
}

// §10.6, the dna-assembly replay: the task goes in with the prompt, a todo is
// pushed and popped with a result that calls the design validated while
// saying nothing of the unchecked requirement; back at the root, step shows
// the kept list, and again after a later pop, under lean too.
func TestKeptFactsShownAtRootAfterPop(t *testing.T) {
	s := withTexts(t)
	ss := s.session("S")
	rootEntries := `[{"type":"user","uuid":"u1","sessionId":"S","source":"prompt","message":{"role":"user","content":[{"type":"text","text":"x"}]}}]`
	step := `{"op":"step","session":"S","used":1000,"point":96000,"lean":true,"entries":` + rootEntries + `}`
	push := func(id string) string {
		return `{"op":"call","session":"S","call_id":"` + id + `","name":"t","input":{"frames":[{"kind":"push","id":"` + id + `","session":"S"}]}}`
	}
	pop := func(id, result string) string {
		return `{"op":"call","session":"S","call_id":"p` + id + `","name":"t","input":{"frames":[{"kind":"pop","id":"` + id + `","result":` + jsonString(result) + `,"session":"S"}]}}`
	}
	reps, err := serve(t, s, hello,
		`{"op":"prompt","session":"S","text":`+jsonString(dnaTask)+`,"keep":true,"lean":true}`,
		push("design"), pop("design", "Designed 8 primers and validated them; the NEB page could not be fetched."), step,
		push("write"), pop("write", "Wrote primers.fasta."), step, step)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Join(reps[4].Parts, "\n")
	if !strings.Contains(first, "re-check") || !strings.Contains(first, "(still to confirm) If you aren't familiar with BsaI-HF v2") || !strings.Contains(first, "(left unchecked by a closed todo) the NEB page could not be fetched") {
		t.Errorf("step after the first pop:\n%s", first)
	}
	if again := strings.Join(reps[7].Parts, "\n"); !strings.Contains(again, "(still to confirm)") {
		t.Errorf("step after a later pop should show the list again:\n%s", again)
	}
	if reps[8].Parts != nil && len(reps[8].Parts) != 0 {
		t.Errorf("no pop since: nothing to say, got %q", reps[8].Parts)
	}
	if ss.task == "" {
		t.Error("the task was not kept")
	}
}

// Without keep, step is as it was: the verify note and no list.
func TestNoKeptListWithoutKeep(t *testing.T) {
	s := withTexts(t)
	rootEntries := `[{"type":"user","uuid":"u1","sessionId":"S","message":{"role":"user","content":[{"type":"text","text":"x"}]}}]`
	reps, err := serve(t, s, hello,
		`{"op":"prompt","session":"S","text":`+jsonString(dnaTask)+`,"lean":true}`,
		`{"op":"call","session":"S","call_id":"a","name":"t","input":{"frames":[{"kind":"push","id":"f","session":"S"}]}}`,
		`{"op":"call","session":"S","call_id":"b","name":"t","input":{"frames":[{"kind":"pop","id":"f","result":"done","session":"S"}]}}`,
		`{"op":"step","session":"S","used":1000,"point":96000,"lean":true,"entries":`+rootEntries+`}`)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(reps[4].Parts, "\n")
	if !strings.Contains(got, "re-check") || strings.Contains(got, "Kept from the task") {
		t.Errorf("got %q", got)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
