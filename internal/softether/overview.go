package softether

import (
	"context"
	"strings"
)

type HubOverview struct {
	HubName      string `json:"hubName"`
	OnlineCount  int    `json:"onlineCount"`
	Container    string `json:"container"`
	Enabled      bool   `json:"enabled"`
	HubStatusRaw string `json:"hubStatusRaw,omitempty"`
}

func (c *Client) HubOverview(ctx context.Context, onlineCount int) (*HubOverview, error) {
	out := &HubOverview{
		HubName:     c.Hub,
		OnlineCount: onlineCount,
		Container:   c.Container,
		Enabled:     c.Enabled,
	}
	if !c.Enabled {
		return out, nil
	}
	raw, err := c.vpncmd(ctx, "/CMD", "HubList")
	if err != nil {
		return out, err
	}
	out.HubStatusRaw = strings.TrimSpace(raw)
	return out, nil
}
