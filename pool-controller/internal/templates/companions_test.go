package templates

import "testing"

func TestCompanionCatalogRejectsUnsafeOrIncompleteGroups(t *testing.T) {
	for name, body := range map[string]string{
		"unknown service":    `  env: {URL: "http://{{service.absent}}"}`,
		"malformed service":  `  env: {URL: "{{service.bad name}}"}`,
		"unsafe cache":       `  caches: {models: /dev}`,
		"overlap":            `  caches: {models: /models, sub: /models/sub}`,
		"invalid cache name": `  caches: {"../../bad": /models}`,
		"missing image":      "  companions:\n    - {name: engine}",
		"reserved name":      "  companions:\n    - {name: main, image: {nvidia: engine:1}}",
		"duplicate":          "  companions:\n    - {name: engine, image: {nvidia: engine:1}}\n    - {name: engine, image: {nvidia: engine:1}}",
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeTemplates(t, map[string]string{"t.yaml": validTemplate("chat", "openai:chat-completions", "chat") + "runner_compose:\n  image: {nvidia: proxy:1}\n" + body + "\n"})
			if _, err := Load(dir); err == nil {
				t.Fatal("accepted invalid group")
			}
		})
	}
}

func TestRunnerSecretDeclarations(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate":           "  secret_env: [KEY, KEY]",
		"invalid":             "  secret_env: [bad-name]",
		"literal collision":   "  secret_env: [KEY]\n  env: {KEY: literal}",
		"generated collision": "  secret_env: [LIVEPEER_PUBLIC_URL]",
		"undeclared bearer":   "  secret_env: [KEY]\n  local_bearer_env: OTHER",
		"manual placeholder":  "  env: {KEY: '{{secret.KEY}}'}",
		"unmanaged":           "  secret_env: [KEY]\n  internal_url: http://external:8080",
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeTemplates(t, map[string]string{"t.yaml": validTemplate("live", "video:transcode.live", "live") + "runner_compose:\n  image: {nvidia: runner:1}\n" + body + "\n"})
			if _, err := Load(dir); err == nil {
				t.Fatal("accepted invalid secret declaration")
			}
		})
	}
	dir := writeTemplates(t, map[string]string{"t.yaml": validTemplate("live", "video:transcode.live", "live") + "runner_compose:\n  image: {nvidia: runner:1}\n  secret_env: [KEY, TOKEN]\n  local_bearer_env: TOKEN\n"})
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
}
