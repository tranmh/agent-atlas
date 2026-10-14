package scenario

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbeddedCatalogIsValid(t *testing.T) {
	c, err := LoadEmbedded()
	require.NoError(t, err)
	require.Len(t, c.Scenarios, 5)
	codes := []string{}
	for _, s := range c.Scenarios {
		codes = append(codes, s.Code)
		require.Len(t, s.Agents, 4, s.ID)
	}
	require.Equal(t, []string{"ex", "kh", "pm", "sc", "tr"}, codes)

	s, a := c.FindAgent("sc-spare-parts")
	require.Equal(t, "service-case", s.ID)
	require.Equal(t, FeatureMultiTurn, a.Feature)
	require.Equal(t, "sc", c.FindOrchestrator("sc-orchestrator").Code)
	require.Equal(t, 2500, int(c.ByCode("sc").Agents[0].Script[0].Wait.Std().Milliseconds()))
}

func TestValidateRejectsBrokenScenario(t *testing.T) {
	_, err := Parse([]byte(`
id: broken
code: bx
title: Broken
color: "#123456"
orchestrator: {id: bx-orchestrator, name: O, city: X, lat: 1, lon: 1}
agents:
  - {id: bx-a, name: A, city: X, lat: 1, lon: 1, feature: streaming, skills: [{id: s}], script: [{say: hi}]}
plan:
  - {step: one, agent: bx-nope, call: send, skill: s, text: t, say: s}
`))
	require.Error(t, err)
	for _, want := range []string{"must end with complete or fail", "unknown agent", "exactly one agent with feature multi_turn", "exactly one agent with mode llm"} {
		require.True(t, strings.Contains(err.Error(), want), "missing %q in %v", want, err)
	}
}

func TestUnknownKeysAreErrors(t *testing.T) {
	_, err := Parse([]byte("id: x\ncode: xx\nbogus: 1\n"))
	require.ErrorContains(t, err, "bogus")
}

func TestEveryScenarioHasTwoMCPServers(t *testing.T) {
	c, err := LoadEmbedded()
	require.NoError(t, err)
	for _, s := range c.Scenarios {
		require.Len(t, s.MCPServers, 2, s.ID)
		roles := []string{s.MCPServers[0].Role, s.MCPServers[1].Role}
		require.ElementsMatch(t, []string{"data", "ops"}, roles, s.ID)
		for _, m := range s.MCPServers {
			require.NotContains(t, m.Target(), "-", m.ID)
			require.Equal(t, m.ID, s.Code+"-"+m.Target())
		}
	}
	s, m := c.FindMCPServer("sc-erp")
	require.Equal(t, "sc", s.Code)
	require.True(t, m.Tool("restricted_shutdown_turbine").Write)
}

func TestMCPValidation(t *testing.T) {
	c, err := LoadEmbedded()
	require.NoError(t, err)
	s := *c.ByCode("sc")
	servers := append([]MCPServer(nil), s.MCPServers...)
	servers[1].Tools = append([]MCPTool(nil), servers[1].Tools...)
	servers[1].Tools[0].Write = true // check_stock marked as write, but not named restricted_*
	s.MCPServers = servers
	err = s.Validate()
	require.ErrorContains(t, err, `write tools and only write tools start with "restricted_"`)
	require.ErrorContains(t, err, "exactly one MCP write tool")
}

// Review C2: scripted tool arguments must match the tool's parameters.
func TestToolStepArgumentsAreValidated(t *testing.T) {
	c, err := LoadEmbedded()
	require.NoError(t, err)
	mutate := func(agentID string, edit func(*ToolCall)) error {
		s := *c.ByCode("sc")
		s.Agents = append([]Agent(nil), s.Agents...)
		for i := range s.Agents {
			if s.Agents[i].ID != agentID {
				continue
			}
			s.Agents[i].Script = append([]Step(nil), s.Agents[i].Script...)
			for j, st := range s.Agents[i].Script {
				if st.Tool != nil && st.Tool.Expect != "denied" {
					tc := *st.Tool
					tc.Args = map[string]any{}
					for k, v := range st.Tool.Args {
						tc.Args[k] = v
					}
					edit(&tc)
					s.Agents[i].Script[j].Tool = &tc
					break
				}
			}
		}
		return s.Validate()
	}
	err = mutate("sc-spare-parts", func(tc *ToolCall) { tc.Args = map[string]any{"partx": "SKF 23148"} })
	require.ErrorContains(t, err, `check_stock: missing argument "part"`)
	require.ErrorContains(t, err, `check_stock: unknown argument "partx"`)

	err = mutate("sc-field-service", func(tc *ToolCall) {}) // unchanged: valid
	require.NoError(t, err)
}

func TestToolArgumentTypesAndEnums(t *testing.T) {
	tool := &MCPTool{Name: "read_trend", Params: []MCPParam{
		{Name: "asset", Type: "string"}, {Name: "signal", Type: "string", Enum: []string{"bearing_temp", "vibration"}}, {Name: "days", Type: "integer"},
	}}
	require.Empty(t, CheckArgs(tool, map[string]any{"asset": "T3", "signal": "vibration", "days": 7}))
	require.Empty(t, CheckArgs(tool, map[string]any{"asset": "{{input}}", "signal": "vibration", "days": 7}))
	problems := CheckArgs(tool, map[string]any{"asset": "T3", "signal": "bogus", "days": "seven"})
	require.Contains(t, problems, `argument "signal" must be one of [bearing_temp vibration]`)
	require.Contains(t, problems, `argument "days" must be a integer`)
}
