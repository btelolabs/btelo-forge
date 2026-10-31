package ui

import (
	"bytes"
	"image/png"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/mind"
)

// TestMCPAddsUnderATask: a topic added under a task is one, unless the call says not, and so is an outline's.
func TestMCPAddsUnderATask(t *testing.T) {
	x := newHarness(t)
	s := connectMCPAt(t, x, "/mind")
	var made mindStateView
	s.ok("mind_new", map[string]any{"title": "Plan", "topic": "Launch"}, &made)
	root := made.List[0].ID
	s.ok("mind_update", map[string]any{"nodes": []map[string]any{{"id": root, "task_list": true}}}, nil)
	var added struct {
		IDs map[string]mind.ID `json:"ids"`
	}
	s.ok("mind_add", map[string]any{
		"parent":  root,
		"outline": "- from an outline",
		"nodes": []map[string]any{
			{"key": "new", "parent": root, "text": "New"},
			{"key": "not", "parent": root, "text": "Not a task", "task": false},
			{"key": "free", "text": "Free", "x": 900, "y": 0},
		},
	}, &added)
	var state mindStateView
	s.ok("mind_state", map[string]any{}, &state)
	tasks := map[string]bool{}
	for _, n := range state.List {
		tasks[n.Text] = n.Task
	}
	if !tasks["New"] || !tasks["from an outline"] || tasks["Not a task"] || tasks["Free"] {
		t.Fatalf("tasks: %v", tasks)
	}
}

// TestMCPBuildsAMindMap drives the mind map server as an AI would: a map with a tree, a flowchart and a table built
// in one call, read back, changed, drawn, undone.
func TestMCPBuildsAMindMap(t *testing.T) {
	x := newHarness(t)
	s := connectMCPAt(t, x, "/mind")
	var made mindStateView
	s.ok("mind_new", map[string]any{"title": "Plan", "topic": "Launch"}, &made)
	if made.Nodes != 1 || made.Title != "Plan" {
		t.Fatalf("mind_new: %+v", made)
	}
	root := made.List[0].ID
	var added struct {
		IDs   map[string]int `json:"ids"`
		Added []int          `json:"added"`
	}
	s.ok("mind_add", map[string]any{
		"nodes": []map[string]any{
			{"key": "goals", "parent": root, "text": "Goals"},
			{"parent_key": "goals", "text": "Reach 10k users"},
			{"parent_key": "goals", "text": "Ship by May"},
			{"key": "risks", "parent": root, "text": "Risks"},
			{"key": "start", "text": "Start", "shape": "capsule", "x": 900, "y": 0},
			{"key": "ask", "text": "Ready?", "shape": "diamond", "x": 900, "y": 160},
			{"key": "ship", "text": "Ship", "shape": "parallelogram", "x": 1150, "y": 170},
			{"key": "budget", "kind": "table", "x": 900, "y": 360, "cells": [][]string{{"Item", "Cost"}, {"Ads", "1200"}, {"Events", "800"}, {"Total", "=SUM(B2:B3)"}}},
		},
		"connectors": []map[string]any{
			{"from_key": "start", "to_key": "ask"},
			{"from_key": "ask", "to_key": "ship", "label": "yes", "route": "elbow"},
			{"from_key": "risks", "to_key": "ask", "dashed": true},
		},
	}, &added)
	if len(added.Added) != 8 || len(added.IDs) != 6 {
		t.Fatalf("mind_add: %+v", added)
	}
	var state mindStateView
	s.ok("mind_state", map[string]any{}, &state)
	if state.Nodes != 9 || state.Connectors != 3 {
		t.Fatalf("%d nodes and %d connectors", state.Nodes, state.Connectors)
	}
	var table *mindNodeView
	for i, n := range state.List {
		if n.ID == 0 {
			t.Fatal("a node without an id")
		}
		if n.Kind == "table" {
			table = &state.List[i]
		}
	}
	if table == nil || table.Values[3][1] != "2000" {
		t.Fatalf("the table as shown: %+v", table)
	}
	// Paged, and narrowed to a subtree.
	var page mindStateView
	s.ok("mind_state", map[string]any{"root": added.IDs["goals"], "connectors": false}, &page)
	if len(page.List) != 3 || page.List[0].Text != "Goals" {
		t.Fatalf("the goals subtree: %+v", page.List)
	}
	s.ok("mind_state", map[string]any{"limit": 2}, &page)
	if len(page.List) != 2 || !page.More {
		t.Fatalf("a page of 2: %d nodes, more %v", len(page.List), page.More)
	}
	// Changes, cells and a move, each one step.
	s.ok("mind_update", map[string]any{"nodes": []map[string]any{{"id": added.IDs["risks"], "text": "Risks and costs", "fill": "#3a2020"}}}, nil)
	s.ok("mind_set_cells", map[string]any{"cells": []map[string]any{{"id": added.IDs["budget"], "row": 2, "col": 1, "value": "1800"}}}, nil)
	s.ok("mind_move", map[string]any{"moves": []map[string]any{{"id": added.IDs["risks"], "parent": added.IDs["goals"]}}}, nil)
	md := x.a.board
	if n := md.m.Node(mind.ID(added.IDs["risks"])); n == nil || n.Parent != mind.ID(added.IDs["goals"]) || n.Text != "Risks and costs" {
		t.Fatalf("after the changes: %+v", n)
	}
	var outline map[string]string
	s.ok("mind_outline", map[string]any{"ids": []int{int(root)}}, &outline)
	if want := "- Launch\n  - Goals\n    - Reach 10k users\n    - Ship by May\n    - Risks and costs\n"; outline["outline"] != want {
		t.Fatalf("outline\n%s", outline["outline"])
	}
	var found []mindNodeView
	s.ok("mind_find", map[string]any{"query": "ship"}, &found)
	if len(found) != 2 {
		t.Fatalf("found %d nodes with ship", len(found))
	}
	// A picture of the result.
	res := s.call("mind_render", map[string]any{"max_size": 800})
	img, ok := res.Content[0].(*mcp.ImageContent)
	if res.IsError || !ok {
		t.Fatalf("mind_render: %+v", res.Content)
	}
	pic, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil || max(pic.Bounds().Dx(), pic.Bounds().Dy()) != 800 {
		t.Fatalf("the picture: %v %v", pic.Bounds(), err)
	}
	// A batch that fails part way changes nothing.
	before := md.m.Len()
	res = s.call("mind_add", map[string]any{"nodes": []map[string]any{{"text": "fine", "x": 0, "y": 900}, {"text": "bad", "parent_key": "nowhere"}}})
	if !res.IsError || md.m.Len() != before {
		t.Fatalf("a failed batch left %d nodes, from %d", md.m.Len(), before)
	}
	// Undo takes back the move.
	s.ok("mind_history", map[string]any{"action": "undo"}, nil)
	if md.m.Node(mind.ID(added.IDs["risks"])).Parent != root {
		t.Fatal("undo did not take back the move")
	}
	s.ok("mind_show", map[string]any{"ids": []int{added.IDs["ship"]}}, nil)
	if !slices.Contains(md.chosen(), mind.ID(added.IDs["ship"])) {
		t.Fatal("mind_show did not choose the node")
	}
	var saved map[string]string
	s.ok("mind_save", map[string]any{}, &saved)
	if saved["path"] != md.path {
		t.Fatalf("saved %q", saved["path"])
	}
	x.settleMind()
	x.shot("mind-mcp")
}
