package connector

import "fmt"

type Config struct {
	URL   string
	Token string
}

func LoadConfig(getenv func(string) string, flagURL string) (Config, bool, error) {
	url := flagURL
	if url == "" {
		url = getenv("AGENTUNNEL_RELAY_URL")
	}
	if url == "" {
		return Config{}, false, nil
	}

	token := getenv("AGENTUNNEL_RELAY_TOKEN")
	if token == "" {
		return Config{}, false, fmt.Errorf("AGENTUNNEL_RELAY_TOKEN is required when relay mode is enabled")
	}

	return Config{
		URL:   url,
		Token: token,
	}, true, nil
}
