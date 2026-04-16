package domain

type Agent struct {
	ID                     string    `json:"id" yaml:"id"`
	Type                   string    `json:"type" yaml:"type"` // main | sub
	Name                   string    `json:"name" yaml:"name"`
	Description            string    `json:"description,omitempty" yaml:"description,omitempty"`
	AvatarURL              string    `json:"avatarUrl,omitempty" yaml:"avatar_url,omitempty"`
	LLM                    LLMConfig `json:"llm" yaml:"llm"`
	SystemPrompt           string    `json:"systemPrompt,omitempty" yaml:"system_prompt,omitempty"`
	PromptModules          []string  `json:"promptModules,omitempty" yaml:"prompt_modules,omitempty"`
	EnabledBuiltinTools    []string  `json:"enabledBuiltinTools,omitempty" yaml:"enabled_builtin_tools,omitempty"`
	EnabledMcpChannelNames []string  `json:"enabledMcpChannelNames,omitempty" yaml:"enabled_mcp_channel_names,omitempty"`
	EnabledSkillNames      []string  `json:"enabledSkillNames,omitempty" yaml:"enabled_skill_names,omitempty"`
	Priority               int       `json:"priority" yaml:"priority"`
}

type LLMConfig struct {
	Provider    string   `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model       string   `json:"model,omitempty" yaml:"model,omitempty"`
	Temperature *float64 `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	MaxTokens   int      `json:"maxTokens,omitempty" yaml:"max_tokens,omitempty"`
}
