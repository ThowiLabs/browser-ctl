package engine

import (
	"github.com/thovilabs/browser-ctl/internal/cdp"
)

// Resaltado del objetivo con el dominio CDP Overlay: el resaltado lo
// dibuja EL NAVEGADOR, no toca el DOM — la página no lo ve ni con
// MutationObserver ni en innerText, y sale en las capturas. Es
// best-effort: si Overlay falla (navegador viejo, dominio no
// habilitado), la acción sigue exactamente como antes.

// highlightConfig es el estilo del resaltado: relleno azul translúcido
// con borde azul sólido, sin caja de información.
var highlightConfig = map[string]any{
	"showInfo":     false,
	"contentColor": map[string]any{"r": 37, "g": 99, "b": 235, "a": 0.18},
	"borderColor":  map[string]any{"r": 37, "g": 99, "b": 235, "a": 0.9},
}

// highlightTarget dibuja el resaltado del navegador alrededor del
// objetivo. Las refs de snapshot usan su backend DOM node id
// (Overlay.highlightNode); lo demás cae al cuadro en pantalla
// (Overlay.highlightRect).
func highlightTarget(e *Engine, client *cdp.Client, sess *Session, target string) {
	if id, ok := refBackendNode(sess, target); ok {
		if _, err := client.Call("Overlay.highlightNode", map[string]any{
			"backendNodeId":   id,
			"highlightConfig": highlightConfig,
		}); err == nil {
			return
		}
	}
	b, err := resolveBoxSess(e, client, sess, target)
	if err != nil {
		return
	}
	_, _ = client.Call("Overlay.highlightRect", map[string]any{
		"x": b.X, "y": b.Y, "width": b.W, "height": b.H,
		"highlightConfig": highlightConfig,
	})
}

// hideHighlight quita el resaltado del navegador, best-effort.
func hideHighlight(client *cdp.Client) {
	_, _ = client.Call("Overlay.hideHighlight", map[string]any{})
}

// refBackendNode devuelve el backend DOM node id de una ref de snapshot
// o de un locator que resuelve a una, o false cuando no hay ninguno.
func refBackendNode(sess *Session, target string) (int, bool) {
	if sess == nil {
		return 0, false
	}
	ref := target
	if !refRe.MatchString(target) {
		var ok bool
		if ref, ok = locatorAXRef(sess, target); !ok {
			return 0, false
		}
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	id, ok := sess.LastRefNodes[ref]
	if !ok || id == 0 {
		return 0, false
	}
	return id, true
}
