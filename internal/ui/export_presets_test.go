package ui

import (
	"testing"

	"github.com/btelolabs/btelo-video-cut/internal/project"
)

func TestEveryPresetIsRecognisedAsItself(t *testing.T) {
	for _, av1 := range []bool{false, true} {
		for i, p := range exportPresets {
			e := p.export(av1)
			if !e.Valid() {
				t.Fatalf("%s is not a valid export: %+v", p.id, e)
			}
			if got := matchingPreset(e, av1); got != i {
				t.Fatalf("%s (av1 %v) is recognised as %d", p.id, av1, got)
			}
		}
	}
	size := exportPresets[len(exportPresets)-1].export(false)
	size.TargetMB = 8
	if exportPresets[matchingPreset(size, false)].id != "size" {
		t.Fatal("the size preset should match any target size")
	}
	if matchingPreset(project.Export{Quality: project.Smallest, FPS: 60}, false) != -1 {
		t.Fatal("a mix of settings should be custom")
	}
	if len(qualityNames) != len(project.Qualities) || len(qualityIDs) != len(project.Qualities) || len(audioNames) != len(audioIDs) ||
		len(speedNames) != len(speedOrder) || len(codecNames) != len(codecIDs) || len(heightNames) != len(exportHeights) || len(rateNames) != len(exportRates) {
		t.Fatal("an option list and its labels differ in length")
	}
}
