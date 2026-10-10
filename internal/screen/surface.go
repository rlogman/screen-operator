package screen

import (
	"fmt"
	"time"

	"screenoperator/internal/config"
)

// Surface es la vista tipada de surface.* (config/surfaces/*.yaml).
type Surface struct {
	ID        string `json:"id"`
	Transport string `json:"transport"`
	RFB       struct {
		Auth      string   `json:"auth"`
		Shared    bool     `json:"shared"`
		Encodings []string `json:"encodings"`
	} `json:"rfb"`
	Screen struct {
		Width           int  `json:"width"`
		Height          int  `json:"height"`
		TrustServerInit bool `json:"trust_server_init"`
	} `json:"screen"`
	Input  InputConfig  `json:"input"`
	Settle SettleConfig `json:"settle"`
}

// InputConfig es surface.input.
type InputConfig struct {
	ClickHoldMS      int `json:"click_hold_ms"`
	DoubleClickGapMS int `json:"double_click_gap_ms"`
	KeyHoldMS        int `json:"key_hold_ms"`
	TypeCharDelayMS  int `json:"type_char_delay_ms"`
	ReplaceText      struct {
		Strategy string   `json:"strategy"`
		Chord    []string `json:"chord"`
	} `json:"replace_text"`
	Scroll struct {
		WheelClicksPerStep int `json:"wheel_clicks_per_step"`
	} `json:"scroll"`
	HoverDwellMS int `json:"hover_dwell_ms"`
}

// SettleConfig es surface.settle.
type SettleConfig struct {
	MinWaitMS       int     `json:"min_wait_ms"`
	MaxWaitMS       int     `json:"max_wait_ms"`
	PollMS          int     `json:"poll_ms"`
	StableFrames    int     `json:"stable_frames"`
	DiffThreshold   float64 `json:"diff_threshold"`
	ChangeHorizonMS int     `json:"change_horizon_ms"`
}

// Perception es perception.* (config/base.yaml).
type Perception struct {
	Screenshot struct {
		Format      string `json:"format"`
		JPEGQuality int    `json:"jpeg_quality"`
		MaxWidth    int    `json:"max_width"`
	} `json:"screenshot"`
	Zoom struct {
		Enabled    bool    `json:"enabled"`
		Factor     float64 `json:"factor"`
		MaxPerStep int     `json:"max_per_step"`
	} `json:"zoom"`
}

// FromConfig extrae superficie y percepcion de una configuracion resuelta.
func FromConfig(c *config.Config) (Surface, Perception, error) {
	var s Surface
	var p Perception
	if err := c.Decode("surface", &s); err != nil {
		return s, p, fmt.Errorf("screen: surface: %w", err)
	}
	if err := c.Decode("perception", &p); err != nil {
		return s, p, fmt.Errorf("screen: perception: %w", err)
	}
	return s, p, nil
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }
