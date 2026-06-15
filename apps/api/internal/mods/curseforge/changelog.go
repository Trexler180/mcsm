package curseforge

import (
	"context"

	"github.com/mcsm/api/internal/mods/htmlmd"
)

// changelog fetches a file's changelog through the Core API (keyed or proxy)
// and converts it to Markdown. CurseForge serves changelogs as author-written
// HTML; the frontend renders Markdown only, so the HTML is converted rather
// than passed through (also avoids handing author-controlled HTML to the
// browser).
func (c *Client) changelog(ctx context.Context, projectID, fileID string) (string, error) {
	var raw struct {
		Data string `json:"data"`
	}
	if err := c.get(ctx, "/v1/mods/"+projectID+"/files/"+fileID+"/changelog", nil, &raw); err != nil {
		return "", err
	}
	return htmlmd.ToMarkdown(raw.Data), nil
}
