package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tranmh/agent-atlas/scenarios"
)

// Catalog is the set of all scenarios, in a stable order (by code).
type Catalog struct {
	Scenarios []*Scenario
}

// LoadEmbedded parses and validates the scenario files compiled into the binary.
func LoadEmbedded() (*Catalog, error) { return LoadFS(scenarios.FS) }

// LoadFS parses and validates every *.yaml file at the root of fsys.
func LoadFS(fsys fs.FS) (*Catalog, error) {
	names, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, err
	}
	c := &Catalog{}
	for _, n := range names {
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			return nil, err
		}
		s, err := Parse(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		c.Scenarios = append(c.Scenarios, s)
	}
	sort.Slice(c.Scenarios, func(i, j int) bool { return c.Scenarios[i].Code < c.Scenarios[j].Code })
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Parse decodes one scenario (unknown keys are errors) and validates it.
func Parse(b []byte) (*Scenario, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var s Scenario
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	for i := range s.Agents {
		if s.Agents[i].Mode == "" {
			s.Agents[i].Mode = ModeScripted
		}
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// ByCode returns the scenario with the given code or id.
func (c *Catalog) ByCode(code string) *Scenario {
	for _, s := range c.Scenarios {
		if s.Code == code || s.ID == code {
			return s
		}
	}
	return nil
}

// FindAgent returns the scenario and worker agent with the given id.
func (c *Catalog) FindAgent(id string) (*Scenario, *Agent) {
	for _, s := range c.Scenarios {
		if a := s.Agent(id); a != nil {
			return s, a
		}
	}
	return nil, nil
}

// FindOrchestrator returns the scenario whose orchestrator has the given id.
func (c *Catalog) FindOrchestrator(id string) *Scenario {
	for _, s := range c.Scenarios {
		if s.Orchestrator.ID == id {
			return s
		}
	}
	return nil
}

// Agent returns the worker agent with the given id, or nil.
func (s *Scenario) Agent(id string) *Agent {
	for i := range s.Agents {
		if s.Agents[i].ID == id {
			return &s.Agents[i]
		}
	}
	return nil
}

var (
	codeRe  = regexp.MustCompile(`^[a-z]{2}$`)
	idRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// Validate checks one scenario for internal consistency.
func (s *Scenario) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if !idRe.MatchString(s.ID) {
		add("id %q: lower-case letters, digits and dashes", s.ID)
	}
	if !codeRe.MatchString(s.Code) {
		add("code %q: two lower-case letters", s.Code)
	}
	if s.Title == "" {
		add("title is required")
	}
	if !colorRe.MatchString(s.Color) {
		add("color %q: #rrggbb", s.Color)
	}
	checkPlace := func(what string, p Place) {
		if !idRe.MatchString(p.ID) || !strings.HasPrefix(p.ID, s.Code+"-") {
			add("%s id %q: must match %s and start with %q", what, p.ID, idRe, s.Code+"-")
		}
		if p.Name == "" || p.City == "" {
			add("%s %s: name and city are required", what, p.ID)
		}
		if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 || (p.Lat == 0 && p.Lon == 0) {
			add("%s %s: lat/lon out of range", what, p.ID)
		}
	}
	checkPlace("orchestrator", s.Orchestrator)
	if !strings.HasSuffix(s.Orchestrator.ID, "-orchestrator") {
		add("orchestrator id %q must end with -orchestrator", s.Orchestrator.ID)
	}

	seen := map[string]bool{s.Orchestrator.ID: true}
	features := map[Feature]int{}
	llmAgents := 0
	for _, a := range s.Agents {
		checkPlace("agent", a.Place)
		if seen[a.ID] {
			add("duplicate id %q", a.ID)
		}
		seen[a.ID] = true
		features[a.Feature]++
		switch a.Feature {
		case FeatureStreaming, FeatureMultiTurn, FeatureLongTaskPush, FeatureRestricted:
		default:
			add("agent %s: unknown feature %q", a.ID, a.Feature)
		}
		switch a.Mode {
		case ModeScripted:
		case ModeLLM:
			llmAgents++
			if a.LLM == nil || a.LLM.System == "" {
				add("agent %s: mode llm needs llm.system", a.ID)
			}
		default:
			add("agent %s: unknown mode %q", a.ID, a.Mode)
		}
		if len(a.Skills) == 0 {
			add("agent %s: at least one skill", a.ID)
		}
		inputs, terminal, llmSteps := 0, 0, 0
		for i, st := range a.Script {
			if st.actions() != 1 {
				add("agent %s script[%d]: exactly one of say, input_required, artifact, llm, complete, fail", a.ID, i)
			}
			switch st.Kind() {
			case "input_required":
				inputs++
			case "complete", "fail":
				terminal++
				if i != len(a.Script)-1 {
					add("agent %s script[%d]: complete/fail must be the last step", a.ID, i)
				}
			case "llm":
				llmSteps++
				if st.Fallback == "" {
					add("agent %s script[%d]: llm step needs a fallback text", a.ID, i)
				}
			case "artifact":
				if st.Artifact.Name == "" {
					add("agent %s script[%d]: artifact needs a name", a.ID, i)
				}
			}
		}
		if terminal != 1 {
			add("agent %s: script must end with complete or fail", a.ID)
		}
		if a.Feature == FeatureMultiTurn && inputs < 1 {
			add("agent %s: multi_turn needs an input_required step", a.ID)
		}
		if a.Feature != FeatureMultiTurn && inputs > 0 {
			add("agent %s: only multi_turn agents may ask for input", a.ID)
		}
		if a.Mode == ModeLLM && llmSteps == 0 {
			add("agent %s: mode llm needs an llm step", a.ID)
		}
		if a.Feature == FeatureRestricted {
			denied := false
			for _, sk := range a.Skills {
				denied = denied || strings.HasPrefix(sk.ID, DeniedSkillPrefix)
			}
			if !denied {
				add("agent %s: restricted agent needs a %q skill", a.ID, DeniedSkillPrefix)
			}
		}
	}
	for _, f := range []Feature{FeatureStreaming, FeatureMultiTurn, FeatureLongTaskPush, FeatureRestricted} {
		if features[f] != 1 {
			add("exactly one agent with feature %s (have %d)", f, features[f])
		}
	}
	if llmAgents != 1 {
		add("exactly one agent with mode llm (have %d)", llmAgents)
	}

	steps := map[string]bool{}
	for i, p := range s.Plan {
		a := s.Agent(p.Agent)
		switch {
		case p.Step == "" || steps[p.Step]:
			add("plan[%d]: step name missing or duplicate", i)
		case a == nil:
			add("plan %s: unknown agent %q", p.Step, p.Agent)
		default:
			ok := false
			for _, sk := range a.Skills {
				ok = ok || sk.ID == p.Skill
			}
			if !ok {
				add("plan %s: agent %s has no skill %q", p.Step, p.Agent, p.Skill)
			}
			if a.Feature == FeatureMultiTurn && p.Reply == "" && p.Expect == "" {
				add("plan %s: multi_turn agent needs a reply", p.Step)
			}
		}
		steps[p.Step] = true
		switch p.Call {
		case CallSend, CallStream, CallPush:
		default:
			add("plan %s: call must be send, stream or push", p.Step)
		}
		if p.Expect != "" && p.Expect != "denied" {
			add("plan %s: expect must be empty or denied", p.Step)
		}
		if p.Text == "" || p.Say == "" {
			add("plan %s: text and say are required", p.Step)
		}
		if i == 0 && p.Parallel {
			add("plan %s: the first step cannot be parallel", p.Step)
		}
	}
	if len(s.Plan) == 0 {
		add("plan is empty")
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("scenario %s: %w", s.ID, err)
	}
	return nil
}

func (c *Catalog) validate() error {
	codes, ids := map[string]bool{}, map[string]string{}
	var errs []error
	for _, s := range c.Scenarios {
		if codes[s.Code] {
			errs = append(errs, fmt.Errorf("duplicate scenario code %q", s.Code))
		}
		codes[s.Code] = true
		for _, id := range append([]string{s.Orchestrator.ID}, agentIDs(s)...) {
			if other, ok := ids[id]; ok {
				errs = append(errs, fmt.Errorf("id %q used by %s and %s", id, other, s.ID))
			}
			ids[id] = s.ID
		}
	}
	return errors.Join(errs...)
}

func agentIDs(s *Scenario) []string {
	out := make([]string, len(s.Agents))
	for i, a := range s.Agents {
		out[i] = a.ID
	}
	return out
}
