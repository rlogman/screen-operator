package rfb

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ResetURL deriva la URL de reinicio del escritorio a partir de la URL del
// websocket: wss://host/ruta/websockify -> https://host/__reset.
func ResetURL(desktopURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(desktopURL))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("rfb: URL del escritorio invalida")
	}
	scheme := "https"
	switch strings.ToLower(u.Scheme) {
	case "ws", "http":
		scheme = "http"
	case "wss", "https":
	default:
		return "", fmt.Errorf("rfb: esquema %q no soportado", u.Scheme)
	}
	return scheme + "://" + u.Host + "/__reset", nil
}

// Reset llama POST /__reset con Authorization: Bearer <token> y devuelve el
// codigo HTTP. El token nunca aparece en el error.
func Reset(ctx context.Context, client *http.Client, desktopURL, token string) (int, error) {
	if token == "" {
		return 0, fmt.Errorf("rfb: falta el token para /__reset")
	}
	target, err := ResetURL(desktopURL)
	if err != nil {
		return 0, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return 0, fmt.Errorf("rfb: %s", redact(err.Error(), token))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("rfb: POST /__reset fallo: %s", redact(err.Error(), token))
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
