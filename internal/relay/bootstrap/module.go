package bootstrap

import (
	"database/sql"
	"errors"
	"net/http"

	"yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/relay/auth"
	"yuanbohan/tunnel/internal/relay/handler"
	"yuanbohan/tunnel/internal/relay/operator"
	"yuanbohan/tunnel/internal/relay/session"
	"yuanbohan/tunnel/internal/relay/store/postgres"
)

func NewServeHandler(db *sql.DB) (http.Handler, error) {
	if db == nil {
		return nil, errors.New("bootstrap: db is required")
	}

	digester, err := auth.NewSecretDigester(config.RelayAppSecret())
	if err != nil {
		return nil, err
	}

	store := postgres.NewPostgresStore(db)
	registry := session.NewRegistry()
	appAuth := auth.NewAppAuthService(store, digester, auth.DefaultPasswordHasher())
	agentTokens := auth.NewAgentTokenService(store, digester)
	operatorService := operator.NewOperatorService(store)
	serveHandler := handler.New(registry, appAuth, agentTokens, operatorService)

	return serveHandler, nil
}
