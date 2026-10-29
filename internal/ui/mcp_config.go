package ui

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (a *App) addConfigTools(s *mcp.Server) {
	type nameIn struct {
		Name string `json:"name" jsonschema:"music_sources, subtitle_models, speech_voices, ffmpeg or streaming_platforms"`
	}
	tool(s, a, "get_config", "Read an extension configuration as JSON: music_sources (the online music and sound-effect sites), subtitle_models (speech engines, models and languages), ffmpeg (which ffmpeg the app uses and where it downloads it) or streaming_platforms (the live-streaming services offered when adding a destination: their servers and where their stream keys are found). Returns the JSON in use, whether it is the user's own file, and a guide to the format.",
		func(in nameIn) (any, error) {
			k, err := findConfig(in.Name)
			if err != nil {
				return nil, err
			}
			text, custom := a.configText(k)
			return map[string]any{"name": k.id, "json": text, "custom": custom, "file": a.configPath(k), "guide": k.guide}, nil
		})
	type setIn struct {
		Name string `json:"name" jsonschema:"music_sources, subtitle_models, speech_voices, ffmpeg or streaming_platforms"`
		JSON string `json:"json" jsonschema:"the complete new configuration; read it with get_config first and change what is needed"`
	}
	tool(s, a, "set_config", "Replace an extension configuration with new JSON. It is checked first: when it is invalid nothing changes and the error names the place. When it is valid it takes effect at once and is kept for the next start.",
		func(in setIn) (any, error) {
			k, err := findConfig(in.Name)
			if err != nil {
				return nil, err
			}
			if err := a.saveConfig(k, in.JSON); err != nil {
				return nil, err
			}
			a.extensions.forget(k.id)
			return map[string]any{"saved": a.configPath(k), "summary": a.configSummary(k)}, nil
		})
	tool(s, a, "reset_config", "Go back to the built-in configuration (music_sources, subtitle_models, speech_voices, ffmpeg or streaming_platforms), removing the user's file.",
		func(in nameIn) (any, error) {
			k, err := findConfig(in.Name)
			if err != nil {
				return nil, err
			}
			if err := a.resetConfig(k); err != nil {
				return nil, err
			}
			a.extensions.forget(k.id)
			return map[string]any{"reset": k.id, "summary": a.configSummary(k)}, nil
		})
}

// configSummary lists what a configuration now offers, so a tool can confirm the change.
func (a *App) configSummary(k configKind) any {
	switch k.id {
	case "streaming_platforms":
		var ids []string
		for _, p := range a.onAir.platforms.Platforms {
			ids = append(ids, p.ID)
		}
		return map[string]any{"platforms": ids}
	case "ffmpeg":
		return map[string]any{"minimum": a.tools.config.Minimum, "useSystemFFmpeg": a.tools.config.System, "downloads": len(a.tools.config.Downloads)}
	}
	if k.id == "music_sources" {
		var ids []string
		for _, s := range a.lib.Config().Enabled() {
			ids = append(ids, s.ID)
		}
		return map[string]any{"enabledSources": ids}
	}
	c := a.subtitles.config
	var engines, models []string
	for _, e := range c.Available() {
		engines = append(engines, e.ID)
	}
	for _, m := range c.Models {
		models = append(models, m.ID)
	}
	return map[string]any{"enginesOnThisComputer": engines, "models": models, "languages": len(c.Languages)}
}
