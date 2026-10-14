// Package scenario defines the scenario file format shared by every component:
// atlasgen generates compose services and gateway routes from it, agents read
// their card and script from it, orchestrators read their plan from it, and the
// visualizer serves it to the browser (map positions, colours, names).
package scenario

import (
	"fmt"
	"strings"
	"time"
)

// Feature is the one A2A protocol feature a worker agent demonstrates. It also
// selects the gateway policies attached to the agent's route.
type Feature string

const (
	FeatureStreaming    Feature = "streaming"      // SendStreamingMessage; route has retries (chaos demo)
	FeatureMultiTurn    Feature = "multi_turn"     // input-required, second message in the same task
	FeatureLongTaskPush Feature = "long_task_push" // ReturnImmediately + push webhook; route has a delay
	FeatureRestricted   Feature = "restricted"     // route has a CEL authorization policy
)

// Mode says how an agent produces the content of its `llm` steps.
type Mode string

const (
	ModeScripted Mode = "scripted"
	ModeLLM      Mode = "llm"
)

// DeniedSkillPrefix marks skills the gateway's authorization policy refuses.
const DeniedSkillPrefix = "restricted-"

// Scenario is one use case: an orchestrator, four worker agents and a plan.
type Scenario struct {
	ID           string     `yaml:"id" json:"id"`
	Code         string     `yaml:"code" json:"code"` // two letters, prefix of every agent id
	Title        string     `yaml:"title" json:"title"`
	Summary      string     `yaml:"summary" json:"summary"`
	Color        string     `yaml:"color" json:"color"` // #rrggbb, the scenario's hue on the map
	Orchestrator Place      `yaml:"orchestrator" json:"orchestrator"`
	Agents       []Agent    `yaml:"agents" json:"agents"`
	Plan         []PlanStep `yaml:"plan" json:"plan"`
	Closing      string     `yaml:"closing" json:"closing"` // orchestrator's final note
}

// Place is anything with a name at a location (the orchestrator, an agent).
type Place struct {
	ID          string  `yaml:"id" json:"id"`
	Name        string  `yaml:"name" json:"name"`
	Description string  `yaml:"description" json:"description"`
	City        string  `yaml:"city" json:"city"`
	Country     string  `yaml:"country" json:"country"`
	Lat         float64 `yaml:"lat" json:"lat"`
	Lon         float64 `yaml:"lon" json:"lon"`
}

// Agent is a worker agent: a place plus its A2A card content and script.
type Agent struct {
	Place   `yaml:",inline" json:",inline"`
	Feature Feature `yaml:"feature" json:"feature"`
	Mode    Mode    `yaml:"mode" json:"mode"`
	Skills  []Skill `yaml:"skills" json:"skills"`
	LLM     *LLM    `yaml:"llm,omitempty" json:"llm,omitempty"`
	Script  []Step  `yaml:"script" json:"script"`
}

// Skill is an A2A AgentSkill.
type Skill struct {
	ID          string   `yaml:"id" json:"id"`
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// LLM configures how an `llm` step talks to the model.
type LLM struct {
	System    string `yaml:"system" json:"system"`
	MaxTokens int    `yaml:"max_tokens" json:"max_tokens"`
}

// Step is one step of an agent script. Exactly one of the action fields is set.
type Step struct {
	Say           string    `yaml:"say,omitempty" json:"say,omitempty"`                       // working status update
	Wait          Duration  `yaml:"wait,omitempty" json:"wait,omitempty"`                     // pause after the step
	InputRequired string    `yaml:"input_required,omitempty" json:"input_required,omitempty"` // question; the task pauses
	Artifact      *Artifact `yaml:"artifact,omitempty" json:"artifact,omitempty"`
	LLMPrompt     string    `yaml:"llm,omitempty" json:"llm,omitempty"`           // prompt template; answer becomes an artifact
	Fallback      string    `yaml:"fallback,omitempty" json:"fallback,omitempty"` // text used instead of the LLM
	Complete      string    `yaml:"complete,omitempty" json:"complete,omitempty"` // final message, task completed
	Fail          string    `yaml:"fail,omitempty" json:"fail,omitempty"`         // final message, task failed
}

// Kind names the action of a step.
func (s Step) Kind() string {
	switch {
	case s.Say != "":
		return "say"
	case s.InputRequired != "":
		return "input_required"
	case s.Artifact != nil:
		return "artifact"
	case s.LLMPrompt != "":
		return "llm"
	case s.Complete != "":
		return "complete"
	case s.Fail != "":
		return "fail"
	}
	return ""
}

func (s Step) actions() int {
	n := 0
	for _, set := range []bool{s.Say != "", s.InputRequired != "", s.Artifact != nil, s.LLMPrompt != "", s.Complete != "", s.Fail != ""} {
		if set {
			n++
		}
	}
	return n
}

// Artifact is an artifact a step emits: text, structured data, or both.
type Artifact struct {
	Name string         `yaml:"name" json:"name"`
	Text string         `yaml:"text,omitempty" json:"text,omitempty"`
	Data map[string]any `yaml:"data,omitempty" json:"data,omitempty"`
}

// Call is how the orchestrator invokes an agent.
type Call string

const (
	CallSend   Call = "send"   // SendMessage (blocking)
	CallStream Call = "stream" // SendStreamingMessage (SSE)
	CallPush   Call = "push"   // SendMessage with returnImmediately + push webhook, then GetTask
)

// PlanStep is one delegation of the orchestrator.
type PlanStep struct {
	Step     string `yaml:"step" json:"step"`
	Agent    string `yaml:"agent" json:"agent"`
	Call     Call   `yaml:"call" json:"call"`
	Skill    string `yaml:"skill" json:"skill"`
	Text     string `yaml:"text" json:"text"`
	Say      string `yaml:"say" json:"say"`                               // narration shown in the UI
	Reply    string `yaml:"reply,omitempty" json:"reply,omitempty"`       // answer to input-required
	Expect   string `yaml:"expect,omitempty" json:"expect,omitempty"`     // "denied": a 403 is the expected outcome
	Parallel bool   `yaml:"parallel,omitempty" json:"parallel,omitempty"` // runs together with the previous step
}

// Duration is a time.Duration that reads "2.5s" from YAML and writes milliseconds to JSON.
type Duration time.Duration

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON writes the duration in milliseconds.
func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%d", time.Duration(d).Milliseconds())), nil
}

// Std returns the time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }
