package domain_test

// Acceptance scenarios for the allowlist brake, bound to the domain type
// directly: the behaviour under test is the policy decision itself, and
// routing it through an adapter would test the adapter instead.
//
// godog runs in LIBRARY mode under `go test` — its standalone CLI is
// deprecated (cucumber/godog#478), and library mode is what gives this the
// normal exit code, -race, and coverage integration every other suite here
// already has.
//
// Contract for editing this file and features/allowlist.feature: a red
// scenario is a finding about the code, never a reason to soften the
// scenario. See CLAUDE.md rules 2 and 16 and the spec-guard (#338).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/colors"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// allowlistWorld is the per-scenario state. godog constructs one per
// scenario via the Before hook, so scenarios cannot leak into each other.
type allowlistWorld struct {
	list   *domain.Allowlist
	answer bool
	asked  bool
}

func (w *allowlistWorld) anEmptyAllowlist() error {
	w.list = domain.NewAllowlist()
	w.answer = false
	w.asked = false
	return nil
}

// parse keeps the Gherkin at domain level: scenarios name a contact by its
// JID string, and the translation to a domain.JID lives here, not in the
// feature file.
func parse(jid string) (domain.JID, error) {
	j, err := domain.Parse(jid)
	if err != nil {
		return domain.JID{}, fmt.Errorf("scenario names an unparseable contact %q: %w", jid, err)
	}
	return j, nil
}

func parseActions(names ...string) ([]domain.Action, error) {
	out := make([]domain.Action, 0, len(names))
	for _, n := range names {
		a, err := domain.ParseAction(n)
		if err != nil {
			return nil, fmt.Errorf("scenario names an unknown action %q: %w", n, err)
		}
		out = append(out, a)
	}
	return out, nil
}

func (w *allowlistWorld) isGranted(jid, action string) error {
	j, err := parse(jid)
	if err != nil {
		return err
	}
	acts, err := parseActions(action)
	if err != nil {
		return err
	}
	w.list.Grant(j, acts...)
	return nil
}

func (w *allowlistWorld) isGrantedBoth(jid, first, second string) error {
	j, err := parse(jid)
	if err != nil {
		return err
	}
	acts, err := parseActions(first, second)
	if err != nil {
		return err
	}
	w.list.Grant(j, acts...)
	return nil
}

func (w *allowlistWorld) isAskedWhether(jid, action string) error {
	j, err := parse(jid)
	if err != nil {
		return err
	}
	acts, err := parseActions(action)
	if err != nil {
		return err
	}
	w.answer = w.list.Allows(j, acts[0])
	w.asked = true
	return nil
}

func (w *allowlistWorld) isRevokedFrom(action, jid string) error {
	j, err := parse(jid)
	if err != nil {
		return err
	}
	acts, err := parseActions(action)
	if err != nil {
		return err
	}
	w.list.Revoke(j, acts...)
	return nil
}

func (w *allowlistWorld) theAnswerIs(expected string) error {
	if !w.asked {
		return errors.New("scenario asserts an answer but never asked the allowlist")
	}
	want := expected == "permitted"
	if w.answer != want {
		return fmt.Errorf("allowlist answered %s, scenario expects %s",
			verdict(w.answer), expected)
	}
	return nil
}

func (w *allowlistWorld) mayPerform(jid, action string) error {
	if err := w.isAskedWhether(jid, action); err != nil {
		return err
	}
	return w.theAnswerIs("permitted")
}

func (w *allowlistWorld) mayNotPerform(jid, action string) error {
	if err := w.isAskedWhether(jid, action); err != nil {
		return err
	}
	return w.theAnswerIs("refused")
}

func (w *allowlistWorld) holdsContacts(n int) error {
	if got := w.list.Size(); got != n {
		return fmt.Errorf("allowlist holds %d contact(s), scenario expects %d", got, n)
	}
	return nil
}

func verdict(allowed bool) string {
	if allowed {
		return "permitted"
	}
	return "refused"
}

func InitializeAllowlistScenario(sc *godog.ScenarioContext) {
	w := &allowlistWorld{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, w.anEmptyAllowlist()
	})

	sc.Given(`^an empty allowlist$`, w.anEmptyAllowlist)
	sc.Given(`^"([^"]*)" is granted "([^"]*)"$`, w.isGranted)
	sc.Given(`^"([^"]*)" is granted "([^"]*)" and "([^"]*)"$`, w.isGrantedBoth)

	sc.When(`^the allowlist is asked whether "([^"]*)" may "([^"]*)"$`, w.isAskedWhether)
	sc.When(`^"([^"]*)" is revoked from "([^"]*)"$`, w.isRevokedFrom)

	sc.Then(`^the answer is (permitted|refused)$`, w.theAnswerIs)
	sc.Then(`^"([^"]*)" may "([^"]*)"$`, w.mayPerform)
	sc.Then(`^"([^"]*)" may not "([^"]*)"$`, w.mayNotPerform)
	sc.Then(`^the allowlist holds (\d+) contacts$`, w.holdsContacts)
}

func TestAllowlistFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeAllowlistScenario,
		Options: &godog.Options{
			Format: "pretty",
			// Repo-root-relative from internal/domain/.
			Paths:  []string{"../../features"},
			Output: colors.Colored(os.Stdout),
			// Strict: an undefined or pending step fails the run instead of
			// being reported and skipped. Without it a scenario whose steps
			// never bound reads as green.
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("allowlist scenarios failed")
	}
}
