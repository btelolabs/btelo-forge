package ui

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSidePanelCollapsesAndOpensForNewOverlays(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	full := x.a.layout()
	if full.panel.W == 0 {
		t.Fatal("the panel starts collapsed")
	}

	x.a.setPanelHidden(true)
	x.frame(Input{})
	hidden := x.a.layout()
	if hidden.panel.W != 0 || hidden.preview.W != full.preview.W+full.panel.W {
		t.Fatalf("collapsing should give the panel's width to the preview: %+v -> %+v", full, hidden)
	}
	data, err := os.ReadFile(x.a.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var saved settings
	if err := json.Unmarshal(data, &saved); err != nil || !saved.PanelHidden {
		t.Fatalf("the collapsed panel was not remembered: %s", data)
	}

	x.a.addText()
	if x.a.settings.PanelHidden || x.a.tab != tabOverlays {
		t.Fatal("adding text should open the panel on the overlays page, where it is edited")
	}
}
