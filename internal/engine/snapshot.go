package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

// AXEntry is one snapshot node kept for locator resolution: its ref,
// role and accessible name, in document order.
type AXEntry struct {
	Ref       string
	Role      string
	Name      string
	Value     string
	BackendID int
}

// axChecked returns "checked", "unchecked" or "mixed" for checkable
// roles, from the AX "checked" property. "" when not applicable.
func axChecked(n *axNode) (string, bool) {
	switch n.Role.Value {
	case "checkbox", "radio", "switch", "menuitemcheckbox", "menuitemradio":
	default:
		return "", false
	}
	for _, p := range n.Properties {
		if p.Name != "checked" {
			continue
		}
		switch v := p.Value.Value.(type) {
		case bool:
			if v {
				return "checked", true
			}
			return "unchecked", true
		case string:
			return v, true
		}
	}
	return "", false
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

	snap, refs, nodes, ax, _, err := ctx.Engine.axSnapshot(client, depth, interactive)
	if err != nil {
		return "", "", err
	}
	// The ref space belongs to the tab this snapshot was taken on
	// (the client's tab): refs never leak across tabs.
	ctx.Session.setRefs(client.TabID(), refs, nodes, ax)
	return cap(snap, max), "", nil
}

// axSnapshot builds the snapshot from Accessibility.getFullAXTree.
// Returns the rendered snapshot, ref->JS-expr map, ref->backend DOM node
// id map (0 when the node has no backend id), and the nodes in document
// order for locator resolution.
func (e *Engine) axSnapshot(client *cdp.Client, depth int, interactive bool) (string, map[string]string, map[string]int, []AXEntry, []int, error) {
	// Frame tree first, so refs in iframes get the f<index> prefix.
	// The child list is "childFrames" (nested); flatten it in document
	// order: main frame first (index 0), then each child frame.
	type frameTreeNode struct {
		Frame struct {
			ID string `json:"id"`
		} `json:"frame"`
		ChildFrames []frameTreeNode `json:"childFrames"`
	}
	frameIDs := []string{}
	if raw, err := client.Call("Page.getFrameTree", map[string]any{}); err == nil {
		var tree struct {
			FrameTree frameTreeNode `json:"frameTree"`
		}
		if json.Unmarshal(raw, &tree) == nil {
			var flatten func(n frameTreeNode)
			flatten = func(n frameTreeNode) {
				frameIDs = append(frameIDs, n.Frame.ID)
				for _, c := range n.ChildFrames {
					flatten(c)
				}
			}
			flatten(tree.FrameTree)
		}
	}
	if len(frameIDs) == 0 {
		// No frame tree: still fetch the main frame's AX tree.
		frameIDs = []string{""}
	}
	// No depth param: CDP treats an explicit depth of 0 as a shallow
	// tree (root + 2 levels). Omitting it returns the full tree.
	// Right after a navigation Chrome can return a tree that renders
	// no lines yet (it is still building it); retry briefly so a
	// snapshot does not report "(empty page)" on a loaded page.
	refs := map[string]string{}
	nodes := map[string]int{}
	var ax []AXEntry
	var lines []string
	var lineFrames []int
	for attempt := 0; ; attempt++ {
		// One AX tree per frame: getFullAXTree without a frameId only
		// covers the main frame, so iframe contents need their own
		// call. A child frame that fails to answer is skipped, not
		// fatal: the main tree is what the snapshot needs.
		type axFrame struct {
			byID  map[string]*axNode
			roots []*axNode
		}
		var frames []axFrame
		for i, fid := range frameIDs {
			params := map[string]any{}
			if i > 0 {
				params["frameId"] = fid
			}
			raw, err := client.Call("Accessibility.getFullAXTree", params)
			if err != nil {
				if i == 0 {
					return "", nil, nil, nil, nil, err
				}
				continue
			}
			var res struct {
				Nodes []axNode `json:"nodes"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				if i == 0 {
					return "", nil, nil, nil, nil, err
				}
				continue
			}
			byID := map[string]*axNode{}
			for j := range res.Nodes {
				byID[res.Nodes[j].NodeID] = &res.Nodes[j]
			}
			// Find the root (node with no parent).
			isChild := map[string]bool{}
			for _, n := range res.Nodes {
				for _, c := range n.ChildIDs {
					isChild[c] = true
				}
			}
			var roots []*axNode
			for j := range res.Nodes {
				if !isChild[res.Nodes[j].NodeID] {
					roots = append(roots, &res.Nodes[j])
				}
			}
			frames = append(frames, axFrame{byID: byID, roots: roots})
		}
		if len(frames) == 0 {
			return "", nil, nil, nil, nil, fmt.Errorf("snapshot: no AX tree")
		}
		refs = map[string]string{}
		nodes = map[string]int{}
		ax = nil
		next := 1
		lines = nil
		lineFrames = nil
		iframeSeen := 0
		var walk func(n *axNode, level int, byID map[string]*axNode, frameIdx int, nearestBackend int)
		walk = func(n *axNode, level int, byID map[string]*axNode, frameIdx int, nearestBackend int) {
			if depth > 0 && level > depth {
				return
			}
			// A node without its own DOM counterpart (e.g. an
			// InlineTextBox, whose AX id is synthetic) inherits the
			// nearest ancestor's backend node id, so refs to text
			// nodes still resolve precisely (click/get) instead of
			// falling back to a querySelector that misses.
			if n.BackendDOMNodeID != 0 {
				nearestBackend = n.BackendDOMNodeID
			}
			if n.Ignored {
				for _, c := range n.ChildIDs {
					if child, ok := byID[c]; ok {
						walk(child, level, byID, frameIdx, nearestBackend)
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
					// The frame index comes from the tree being walked:
					// only the root of a frame's AX tree carries its
					// frameId, its descendants do not.
					if frameIdx > 0 {
						ref = fmt.Sprintf("f%de%d", frameIdx, next)
					} else {
						ref = fmt.Sprintf("e%d", next)
					}
					next++
					refs[ref] = axNodeExpr(n)
					if nearestBackend != 0 {
						nodes[ref] = nearestBackend
					}
					ax = append(ax, AXEntry{Ref: ref, Role: role, Name: name, Value: n.Value.Value, BackendID: n.BackendDOMNodeID})
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
				if c, ok := axChecked(n); ok {
					line += ": " + c
				}
				lines = append(lines, line)
				lineFrames = append(lineFrames, frameIdx)
			}
			// An iframe's content lives in its own AX tree: walk it
			// nested under the iframe element (Nth iframe in the main
			// tree is the Nth child frame).
			if role == "Iframe" && iframeSeen < len(frames)-1 {
				iframeSeen++
				child := frames[iframeSeen]
				for _, r := range child.roots {
					walk(r, level+1, child.byID, iframeSeen, 0)
				}
			}
			for _, c := range n.ChildIDs {
				if child, ok := byID[c]; ok {
					walk(child, level+1, byID, frameIdx, nearestBackend)
				}
			}
		}
		for _, r := range frames[0].roots {
			walk(r, 0, frames[0].byID, 0, 0)
		}
		// A single root line means the tree is still building (a loaded page
		// always renders more); keep retrying, same as for zero lines.
		if len(lines) > 1 || attempt >= 10 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(lines) == 0 {
		return "(empty page)", refs, nodes, ax, lineFrames, nil
	}
	return strings.Join(lines, "\n"), refs, nodes, ax, lineFrames, nil
}

// axNodeExpr returns a JS expression for an AX node, falling back to a
// role+name query. Precise resolution uses the backend DOM node id
// (the ref table's node ids) via DOM.resolveNode instead. The query covers both the
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
	var matches []string
	// Source 1: the accessibility snapshot (node names and values).
	// The AX tree is fetched per frame, so it also reaches frames the
	// page itself cannot read; rendered lines know their frame and
	// iframe matches are tagged with it.
	snap, _, _, _, lineFrames, axErr := ctx.Engine.axSnapshot(client, 0, false)
	if axErr == nil {
		for i, line := range strings.Split(snap, "\n") {
			if !strings.Contains(strings.ToLower(line), strings.ToLower(text)) {
				continue
			}
			if i < len(lineFrames) && lineFrames[i] > 0 {
				line = fmt.Sprintf("[frame %d] %s", lineFrames[i], line)
			}
			matches = append(matches, line)
		}
	}
	// Source 2: the rendered text itself — main document, same-origin
	// iframes and open shadow roots. AX names fragment long text
	// across inline nodes, so body text that only exists whole in the
	// DOM (an email's verification code, say) is only found here.
	for _, m := range ctx.Engine.deepTextFind(client, text) {
		matches = append(matches, fmt.Sprintf("[%s] %s", m.Where, m.Snippet))
	}
	if len(matches) == 0 {
		if axErr != nil {
			return "", "", axErr
		}
		return "no matches for " + text, "", nil
	}
	return cap(strings.Join(matches, "\n"), max), "", nil
}

// textMatch is one deepTextFind hit: where it was found (main frame,
// an iframe, a shadow root) and a snippet around the match.
type textMatch struct {
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
}

// deepTextFind searches the page's rendered text for text (case
// insensitive) beyond the main document: every same-origin iframe,
// recursively, and every open shadow root. Cross-origin frames are
// skipped — the browser forbids reading them from the page — but
// doFind's AX source still covers them; closed shadow roots are
// unreachable by design and likewise left to the AX source.
func (e *Engine) deepTextFind(client *cdp.Client, text string) []textMatch {
	if text == "" {
		return nil
	}
	js := fmt.Sprintf(`(function(){
var needle=%s;
var out=[];
function snip(t,i){var s=Math.max(0,i-40);var en=Math.min(t.length,i+needle.length+40);return (s>0?"\u2026":"")+t.slice(s,en).replace(/\s+/g," ").trim()+(en<t.length?"\u2026":"");}
function search(t,where){var low=t.toLowerCase();var i=0;while(out.length<20&&(i=low.indexOf(needle,i))>=0){out.push({where:where,snippet:snip(t,i)});i+=needle.length;}}
function walkKids(root,where){var kids=root.childNodes||[];for(var i=0;i<kids.length;i++){walkNode(kids[i],where);}}
function walkNode(n,where){
if(out.length>=20)return;
if(n.nodeType===3){search(n.nodeValue||"",where);return;}
if(n.nodeType!==1&&n.nodeType!==11)return;
if(n.shadowRoot){walkKids(n.shadowRoot,where+" > shadow root");}
walkKids(n,where);
}
function walkDoc(doc,where){
if(!doc)return;
walkKids(doc,where);
var frames=doc.querySelectorAll?doc.querySelectorAll("iframe"):[];
for(var i=0;i<frames.length;i++){
var f=frames[i];var sub=null;
try{sub=f.contentDocument;}catch(err){sub=null;}
if(sub){walkDoc(sub,"frame "+(i+1)+(f.getAttribute("src")?": "+f.getAttribute("src"):""));}
}
}
walkDoc(document,"main frame");
return out;
})()`, jsonString(strings.ToLower(text)))
	raw, err := e.eval(client, js)
	if err != nil {
		return nil
	}
	var out []textMatch
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
