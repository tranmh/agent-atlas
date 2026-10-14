package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
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

// FindMCPServer returns the scenario and MCP server with the given id.
func (c *Catalog) FindMCPServer(id string) (*Scenario, *MCPServer) {
	for _, s := range c.Scenarios {
		if m := s.MCPServer(id); m != nil {
			return s, m
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
				add("agent %s script[%d]: exactly one of say, input_required, artifact, llm, complete, fail, tool, resource", a.ID, i)
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
			case "tool":
				srv := s.MCPServer(st.Tool.Server)
				switch {
				case srv == nil:
					add("agent %s script[%d]: unknown MCP server %q", a.ID, i, st.Tool.Server)
				case srv.Tool(st.Tool.Name) == nil:
					add("agent %s script[%d]: MCP server %s has no tool %q", a.ID, i, st.Tool.Server, st.Tool.Name)
				case srv.Tool(st.Tool.Name).Write != (st.Tool.Expect == "denied"):
					add("agent %s script[%d]: write tools must be called with expect: denied, others without", a.ID, i)
				default:
					for _, p := range CheckArgs(srv.Tool(st.Tool.Name), st.Tool.Args) {
						add("agent %s script[%d]: %s: %s", a.ID, i, st.Tool.Name, p)
					}
				}
			case "resource":
				srv := s.MCPServer(st.Resource.Server)
				found := false
				if srv != nil {
					for _, r := range srv.Resources {
						found = found || r.URI == st.Resource.URI
					}
				}
				if !found {
					add("agent %s script[%d]: unknown MCP resource %s on %q", a.ID, i, st.Resource.URI, st.Resource.Server)
				}
			}
			if len(st.Tools) > 0 || st.PromptFrom != nil {
				if st.Kind() != "llm" {
					add("agent %s script[%d]: tools and prompt_from belong to llm steps", a.ID, i)
				}
				for _, id := range st.Tools {
					if s.MCPServer(id) == nil {
						add("agent %s script[%d]: unknown MCP server %q in tools", a.ID, i, id)
					}
				}
				if p := st.PromptFrom; p != nil {
					srv := s.MCPServer(p.Server)
					found := false
					if srv != nil {
						for _, pr := range srv.Prompts {
							found = found || pr.Name == p.Name
						}
					}
					if !found {
						add("agent %s script[%d]: unknown MCP prompt %s on %q", a.ID, i, p.Name, p.Server)
					}
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
	s.validateMCP(add, seen)
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

var (
	mcpIDRe   = regexp.MustCompile(`^[a-z]{2}-[a-z]+$`)
	mcpNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,40}$`)
)

// validateMCP checks the MCP servers: one data and one ops server, a resource
// and a prompt on the data server, exactly one slow (progress) tool and exactly
// one write tool (on the ops server, named restricted_*).
func (s *Scenario) validateMCP(add func(string, ...any), seen map[string]bool) {
	if len(s.MCPServers) == 0 {
		return // MCP is optional for a scenario
	}
	roles := map[string]int{}
	progress, writes := 0, 0
	for _, m := range s.MCPServers {
		if !mcpIDRe.MatchString(m.ID) || !strings.HasPrefix(m.ID, s.Code+"-") {
			add("MCP server id %q: <code>-<word>, e.g. %s-historian", m.ID, s.Code)
		}
		if seen[m.ID] {
			add("duplicate id %q", m.ID)
		}
		seen[m.ID] = true
		if m.Name == "" || m.City == "" || (m.Lat == 0 && m.Lon == 0) {
			add("MCP server %s: name, city and lat/lon are required", m.ID)
		}
		roles[m.Role]++
		if m.Role != "data" && m.Role != "ops" {
			add("MCP server %s: role must be data or ops", m.ID)
		}
		if len(m.Tools) == 0 {
			add("MCP server %s: at least one tool", m.ID)
		}
		names := map[string]bool{}
		for _, t := range m.Tools {
			if !mcpNameRe.MatchString(t.Name) || names[t.Name] {
				add("MCP server %s: tool name %q invalid or duplicate", m.ID, t.Name)
			}
			names[t.Name] = true
			if t.Result == "" || t.Description == "" {
				add("MCP tool %s/%s: description and result are required", m.ID, t.Name)
			}
			if t.Write != strings.HasPrefix(t.Name, WriteToolPrefix) {
				add("MCP tool %s/%s: write tools and only write tools start with %q", m.ID, t.Name, WriteToolPrefix)
			}
			if t.Write {
				writes++
				if m.Role != "ops" {
					add("MCP tool %s/%s: write tools belong on the ops server", m.ID, t.Name)
				}
			}
			if t.Progress != nil {
				progress++
				if t.Progress.Steps < 2 || t.Progress.Every.Std() <= 0 || t.Progress.Say == "" {
					add("MCP tool %s/%s: progress needs steps >= 2, every and say", m.ID, t.Name)
				}
			}
			for _, p := range t.Params {
				switch p.Type {
				case "string", "integer", "number":
				default:
					add("MCP tool %s/%s: param %s has unknown type %q", m.ID, t.Name, p.Name, p.Type)
				}
			}
		}
		if m.Role == "data" && (len(m.Resources) == 0 || len(m.Prompts) == 0) {
			add("MCP server %s: the data server needs a resource and a prompt", m.ID)
		}
		for _, r := range m.Resources {
			if r.URI == "" || r.Text == "" || !strings.Contains(r.URI, "://") {
				add("MCP server %s: resource needs a uri (scheme://...) and text", m.ID)
			}
		}
		for _, p := range m.Prompts {
			if !mcpNameRe.MatchString(p.Name) || p.Text == "" {
				add("MCP server %s: prompt %q needs a valid name and text", m.ID, p.Name)
			}
		}
	}
	if roles["data"] != 1 || roles["ops"] != 1 {
		add("exactly one data and one ops MCP server (have %v)", roles)
	}
	if progress != 1 {
		add("exactly one MCP tool with progress (have %d)", progress)
	}
	if writes != 1 {
		add("exactly one MCP write tool (have %d)", writes)
	}
}

// CheckArgs compares scripted tool arguments with the tool's parameters: every
// parameter is required, no unknown arguments, enum values and numeric types
// must fit. Values containing a {{template}} are filled in at run time and
// only checked for presence.
func CheckArgs(t *MCPTool, args map[string]any) []string {
	var out []string
	known := map[string]MCPParam{}
	for _, p := range t.Params {
		known[p.Name] = p
		if _, ok := args[p.Name]; !ok {
			out = append(out, fmt.Sprintf("missing argument %q", p.Name))
		}
	}
	names := make([]string, 0, len(args))
	for k := range args {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		p, ok := known[k]
		if !ok {
			out = append(out, fmt.Sprintf("unknown argument %q", k))
			continue
		}
		v := args[k]
		if str, isStr := v.(string); isStr && strings.Contains(str, "{{") {
			continue
		}
		switch p.Type {
		case "integer":
			switch n := v.(type) {
			case int, int64:
			case float64:
				if n != float64(int64(n)) {
					out = append(out, fmt.Sprintf("argument %q must be an integer", k))
				}
			default:
				out = append(out, fmt.Sprintf("argument %q must be an integer", k))
			}
		case "number":
			switch v.(type) {
			case int, int64, float64:
			default:
				out = append(out, fmt.Sprintf("argument %q must be a number", k))
			}
		}
		if len(p.Enum) > 0 && !slices.Contains(p.Enum, fmt.Sprint(v)) {
			out = append(out, fmt.Sprintf("argument %q must be one of %v", k, p.Enum))
		}
	}
	return out
}
