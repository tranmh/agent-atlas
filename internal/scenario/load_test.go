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
