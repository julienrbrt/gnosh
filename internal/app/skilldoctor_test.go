package app

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// /skill-doctor says what each skill costs in the model's window and how
// often it ran, counting the model's Skill calls and the skills typed:
// those never run first, with how to turn them off (DECISIONS 377).
func TestSkillDoctor(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Skill", Args: `{"skill":"deploy"}`}}},
		apptest.Reply{Text: "Deployed."},
		apptest.Reply{Text: "Deployed again."})
	w.Write(w.ConfigDir()+"/skills/deploy/SKILL.md", "---\ndescription: Deploys the service to the staging box.\n---\nDeploy.\n")
	w.Write(w.ConfigDir()+"/skills/old/SKILL.md", "---\ndescription: An old way of cutting a release, kept around in case it is ever wanted again.\n---\nRelease.\n")
	run(t, w, "-y", "-p", "deploy it").wantCode(t, 0)
	run(t, w, "-y", "-p", "/deploy").wantCode(t, 0)
	out := run(t, w, "-p", "/skill-doctor").wantCode(t, 0).out
	has(t, "/skill-doctor", out, "how often it ran in your last", "/old · user · about", "never ran: /skills old off takes it out", "/deploy · user · about", "ran 2 times, last on",
		"1 skill in the model's list never ran and takes about")
	if strings.Index(out, "/old ·") > strings.Index(out, "/deploy ·") {
		t.Errorf("the skill never run is not first:\n%s", out)
	}
	if got := typedSkills("<gnosh>The user ran the skills \"a\", \"b-c\" by typing: /a /b-c x</gnosh>"); strings.Join(got, ",") != "a,b-c" {
		t.Errorf("typedSkills: %q", got)
	}
	w.Model.Done()
}
