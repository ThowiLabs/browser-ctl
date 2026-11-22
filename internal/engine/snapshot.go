package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/thovilabs/browser-ctl/internal/cdp"
)

// Snapshot via the accessibility tree (CDP Accessibility.getFullAXTree),
// the raw-CDP equivalent of Playwright's ariaSnapshotJSON. Refs are
// e1, e2, ... in document order; frame refs are f<frame>e<n>.
// The snapshot also stores each ref's element expression in the session
// so later commands can resolve it.

type axNode struct {
	NodeID           string                 `json:"nodeId"`
	BackendDOMNodeID int                    `json:"backendDOMNodeId"`
	FrameID          string                 `json:"frameId"`
	Role             struct{ Value string } `json:"role"`
	Name             struct{ Value string } `json:"name"`
	Description      struct{ Value string } `json:"description"`
	Value            struct{ Value string } `json:"value"`
	ChildIDs         []string               `json:"childIds"`
	Properties       []struct {
		Name  string `json:"name"`
		Value struct {
			Value any `json:"value"`
		} `json:"value"`
	} `json:"properties"`
	Ignored bool `json:"ignored"`
}

// interactiveRoles are kept by snapshot -i.
var interactiveRoles = map[string]bool{
	"button": true, "link": true, "textbox": true, "searchbox": true,
	"checkbox": true, "radio": true, "combobox": true, "listbox": true,
	"option": true, "menuitem": true, "menuitemcheckbox": true,
	"menuitemradio": true, "tab": true, "switch": true, "slider": true,
	"spinbutton": true, "treeitem": true, "heading": true,
}

func doSnapshot(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	depth := 0
	if d, ok := ctx.Flags["depth"]; ok {
		fmt.Sscanf(d, "%d", &depth)
	}
	_, interactive := ctx.Flags["i"]
	max := readCap(ctx.Flags["max"], func() bool { _, ok := ctx.Flags["max"]; return ok }())

	snap, refs, nodes, err := ctx.Engine.axSnapshot(client, depth, interactive)
	if err != nil {
		return "", "", err
	}
	ctx.Session.mu.Lock()
	ctx.Session.LastRefs = refs
	ctx.Session.LastRefNodes = nodes
	ctx.Session.mu.Unlock()
	return cap(snap, max), "", nil
}

// axSnapshot builds the snapshot from Accessibility.getFullAXTree.
// Returns the rendered snapshot, ref->JS-expr map, and ref->backend DOM
// node id map (0 when the node has no backend id).
func (e *Engine) axSnapshot(client *cdp.Client, depth int, interactive bool) (string, map[string]string, map[string]int, error) {
	// Frame tree first, so refs in iframes get the f<index> prefix.
	frameIndex := map[string]int{}
	if raw, err := client.Call("Page.getFrameTree", map[string]any{}); err == nil {
		var tree struct {
			FrameTree struct {
				Frame struct {
					ID string `json:"id"`
				} `json:"frame"`
				ChildFrameTree []struct {
					Frame struct {
						ID string `json:"id"`
					} `json:"frame"`
				} `json:"childFrameTree"`
			} `json:"frameTree"`
		}
		if json.Unmarshal(raw, &tree) == nil {
			frameIndex[tree.FrameTree.Frame.ID] = 0
			for i, cf := range tree.FrameTree.ChildFrameTree {
				frameIndex[cf.Frame.ID] = i + 1
			}
		}
	}
	// No depth param: CDP treats an explicit depth of 0 as a shallow
	// tree (root + 2 levels). Omitting it returns the full tree.
	raw, err := client.Call("Accessibility.getFullAXTree", map[string]any{})
	if err != nil {
		return "", nil, nil, err
	}
	var res struct {
		Nodes []axNode `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", nil, nil, err
	}
	byID := map[string]*axNode{}
	for i := range res.Nodes {
		byID[res.Nodes[i].NodeID] = &res.Nodes[i]
	}
	// Find the root (node with no parent).
	isChild := map[string]bool{}
	for _, n := range res.Nodes {
		for _, c := range n.ChildIDs {
			isChild[c] = true
		}
	}
	var roots []*axNode
	for i := range res.Nodes {
		if !isChild[res.Nodes[i].NodeID] {
			roots = append(roots, &res.Nodes[i])
		}
	}
	refs := map[string]string{}
	nodes := map[string]int{}
	next := 1
	var lines []string
	var walk func(n *axNode, level int)
	walk = func(n *axNode, level int) {
		if depth > 0 && level > depth {
			return
		}
		if n.Ignored {
			for _, c := range n.ChildIDs {
				if child, ok := byID[c]; ok {
					walk(child, level)
				}
			}
			return
		}
		role := n.Role.Value
		if role == "" {
			role = "generic"
		}
		name := n.Name.Value
		keep := true
		if interactive {
			keep = interactiveRoles[role] || role == "main" || role == "navigation" || role == "banner" || role == "contentinfo"
			if role == "StaticText" {
				keep = false
			}
		}
		ref := ""
		if !interactive || keep {
			// Only interactive/named nodes get refs.
			if role != "StaticText" && role != "generic" {
				if idx, ok := frameIndex[n.FrameID]; ok && idx > 0 {
					ref = fmt.Sprintf("f%de%d", idx, next)
				} else {
					ref = fmt.Sprintf("e%d", next)
				}
				next++
				refs[ref] = axNodeExpr(n)
				if n.BackendDOMNodeID != 0 {
					nodes[ref] = n.BackendDOMNodeID
				}
			}
		}
		if keep {
			line := strings.Repeat("  ", level) + "- " + role
			if name != "" {
				line += " " + strconv_Quote(name)
			}
			if ref != "" {
				line += " [ref=" + ref + "]"
			}
			if n.Value.Value != "" {
				line += ": " + n.Value.Value
			}
			lines = append(lines, line)
		}
		for _, c := range n.ChildIDs {
			if child, ok := byID[c]; ok {
				walk(child, level+1)
			}
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	if len(lines) == 0 {
		return "(empty page)", refs, nodes, nil
	}
	return strings.Join(lines, "\n"), refs, nodes, nil
}

// axNodeExpr returns a JS expression for an AX node, falling back to a
// role+name query. Precise resolution uses the backend DOM node id
// (LastRefNodes) via DOM.resolveNode instead. The query covers both the
// explicit role attribute and the implicit tag for the role: a plain
// [role="link"] misses <a> elements, which have no role attribute.
func axNodeExpr(n *axNode) string {
	role := n.Role.Value
	name := n.Name.Value
	sel := fmt.Sprintf("[role=%q],%s", role, roleTag(role))
	if name != "" {
		return fmt.Sprintf("(function(){var els=Array.from(document.querySelectorAll(%s)); return els.find(function(el){return (el.innerText||el.getAttribute('aria-label')||'').indexOf(%s)>=0;})||null;})()", jsonString(sel), jsonString(name))
	}
	return fmt.Sprintf("document.querySelector(%s)", jsonString(sel))
}

func strconv_Quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func doFind(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	text := ""
	if len(ctx.Args) > 0 {
		text = ctx.Args[0]
	}
	max := readCap(ctx.Flags["max"], func() bool { _, ok := ctx.Flags["max"]; return ok }())
	snap, _, _, err := ctx.Engine.axSnapshot(client, 0, false)
	if err != nil {
		return "", "", err
	}
	var matches []string
	for _, line := range strings.Split(snap, "\n") {
		if strings.Contains(strings.ToLower(line), strings.ToLower(text)) {
			matches = append(matches, line)
		}
	}
	if len(matches) == 0 {
		return "no matches for " + text, "", nil
	}
	return cap(strings.Join(matches, "\n"), max), "", nil
}
