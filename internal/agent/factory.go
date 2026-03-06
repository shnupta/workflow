package agent

import "github.com/shnupta/workflow/internal/config"

// NewRunner returns the appropriate Runner based on the config.
//
// Selection logic:
//  1. If provider is explicitly set to "claude_api" in config, use ClaudeAPI.
//  2. If claude_bin is set (or findable in PATH), use ClaudeLocal.
//  3. If anthropic_api_key is set, fall back to ClaudeAPI.
//  4. Otherwise return ClaudeLocal (which will fail at Validate() with a clear error).
func NewRunner(cfg *config.Config) Runner {
	switch cfg.AgentProvider {
	case "claude_api":
		return &ClaudeAPI{
			APIKey:       cfg.AnthropicAPIKey,
			Model:        cfg.AgentModel,
			SystemPrompt: cfg.AgentSystemPrompt,
		}
	case "claude_local", "":
		// Default: try local CLI. If bin is empty it'll search PATH.
		r := &ClaudeLocal{ClaudeBin: cfg.ClaudeBin}
		if err := r.Validate(); err != nil {
			// CLI not available — fall back to API if key is set
			if cfg.AnthropicAPIKey != "" {
				return &ClaudeAPI{
					APIKey:       cfg.AnthropicAPIKey,
					Model:        cfg.AgentModel,
					SystemPrompt: cfg.AgentSystemPrompt,
				}
			}
		}
		return r
	default:
		// Unknown provider — return ClaudeLocal, which will surface a clear error
		return &ClaudeLocal{ClaudeBin: cfg.ClaudeBin}
	}
}
