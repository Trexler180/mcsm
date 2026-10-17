package handlers

import (
	"testing"

	"github.com/mcsm/api/internal/mods/modrinth"
)

func TestModrinthCategoriesForPluginsAreTheModOnes(t *testing.T) {
	cats := []modrinth.Category{
		{Name: "optimization", ProjectType: "mod"},
		{Name: "realistic", ProjectType: "resourcepack"},
		{Name: "economy", ProjectType: "mod"},
	}
	got := modrinthCategoriesFor(cats, "plugin")
	if len(got) != 2 || got[0].Name != "optimization" || got[1].Name != "economy" {
		t.Fatalf("plugin categories = %+v, want the two mod ones", got)
	}
	if len(modrinthCategoriesFor(cats, "")) != 3 {
		t.Fatal("no type should mean every category")
	}
}
