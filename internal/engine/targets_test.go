package engine

import (
	"reflect"
	"testing"
)

func TestParseSpaceLocator(t *testing.T) {
	cases := []struct {
		in   string
		want []LocatorCall
	}{
		{`getByRole button "More information"`, []LocatorCall{{Method: "getByRole", Args: []any{"button", "More information"}}}},
		{`getByRole button`, []LocatorCall{{Method: "getByRole", Args: []any{"button"}}}},
		{`getByRole button {name: "Go"}`, []LocatorCall{{Method: "getByRole", Args: []any{"button", map[string]any{"name": "Go"}}}}},
		{`getByText Hello world`, []LocatorCall{{Method: "getByText", Args: []any{"Hello world"}}}},
		{`getByLabel Email`, []LocatorCall{{Method: "getByLabel", Args: []any{"Email"}}}},
		{`getByTestId submit-btn`, []LocatorCall{{Method: "getByTestId", Args: []any{"submit-btn"}}}},
		{`locator div .foo`, []LocatorCall{{Method: "locator", Args: []any{"div .foo"}}}},
	}
	for _, c := range cases {
		tgt, err := ParseTarget(c.in)
		if err != nil {
			t.Errorf("ParseTarget(%q) error: %v", c.in, err)
			continue
		}
		if tgt.Kind != TargetLocator {
			t.Errorf("ParseTarget(%q) kind = %q, want locator", c.in, tgt.Kind)
			continue
		}
		if !reflect.DeepEqual(tgt.Calls, c.want) {
			t.Errorf("ParseTarget(%q) calls = %#v, want %#v", c.in, tgt.Calls, c.want)
		}
	}
	// CSS con espacios no es locator.
	tgt, err := ParseTarget("div .foo")
	if err != nil || tgt.Kind != TargetCSS {
		t.Errorf("ParseTarget(%q) = kind %q err %v, want CSS", "div .foo", tgt.Kind, err)
	}
	// getByRole sin rol es error, no CSS silencioso.
	if _, err := ParseTarget("getByRole"); err == nil {
		t.Errorf("ParseTarget(%q) should fail", "getByRole")
	}
}

func TestMergeTargetArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"click", []string{"getByRole", "button", "More information"}, []string{`getByRole button More information`}},
		{"click", []string{"e12"}, []string{"e12"}},
		{"click", []string{"e12", "e13"}, []string{"e12", "e13"}}, // no es locator: no se junta
		{"hover", []string{"getByText", "Hello world"}, []string{"getByText Hello world"}},
		{"fill", []string{"getByRole", "textbox", "Email", "a@b.c"}, []string{"getByRole textbox Email", "a@b.c"}},
		{"fill", []string{"e12", "hello world"}, []string{"e12", "hello world"}},
		{"fill", []string{"e12", "hello", "world"}, []string{"e12", "hello", "world"}}, // no es locator: no se junta
		{"select", []string{"getByLabel", "País", "MX"}, []string{"getByLabel País", "MX"}},
		{"get", []string{"count", "getByRole", "button", "Go"}, []string{"count", "getByRole button Go"}},
		{"get", []string{"attr", "href", "getByRole", "link", "Learn more"}, []string{"attr", "href", "getByRole link Learn more"}},
		{"get", []string{"url"}, []string{"url"}},
		{"drag", []string{"getByRole", "button", "A", "e2"}, []string{"getByRole", "button", "A", "e2"}}, // drag no se junta
	}
	for _, c := range cases {
		if got := mergeTargetArgs(c.name, c.args); !reflect.DeepEqual(got, c.want) {
			t.Errorf("mergeTargetArgs(%q, %q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

func TestResolveLocatorAX(t *testing.T) {
	sess := &Session{LastAX: []AXEntry{
		{Ref: "e1", Role: "link", Name: "Learn more"},
		{Ref: "e2", Role: "button", Name: "Enviar"},
		{Ref: "e3", Role: "button", Name: "Enviar formulario"},
		{Ref: "e4", Role: "textbox", Name: "Email"},
		{Ref: "e5", Role: "searchbox", Name: "Buscar"},
		{Ref: "e6", Role: "button", Name: "Cancelar"},
	}}
	cases := []struct {
		target string
		want   string
	}{
		{`getByRole link "Learn more"`, "e1"},
		{`getByRole button "Enviar"`, "e2"}, // primer match en orden de documento
		{`getByRole button "Enviar"`, "e2"},
		{`getByRole button`, "e2"},
		{`getByLabel Email`, "e4"},
		{`getByText Enviar formulario`, "e3"},
		{`getByRole textbox "Buscar"`, "e5"}, // textbox casa con searchbox
	}
	for _, c := range cases {
		ref, ok := locatorAXRef(sess, c.target)
		if !ok || ref != c.want {
			t.Errorf("locatorAXRef(%q) = %q, %v; want %q, true", c.target, ref, ok, c.want)
		}
	}
	// exact: true exige igualdad total.
	exact, _ := ParseTarget(`getByRole('button', {name: 'Enviar', exact: true})`)
	ref, ok := resolveLocatorAX(sess, exact.Calls)
	if !ok || ref != "e2" {
		t.Errorf("exact getByRole = %q, %v; want e2, true", ref, ok)
	}
	exactNo, _ := ParseTarget(`getByRole('button', {name: 'Envia', exact: true})`)
	if _, ok := resolveLocatorAX(sess, exactNo.Calls); ok {
		t.Errorf("exact getByRole 'Envia' should not match")
	}
	// .first()/.last()/.nth() sobre los matches.
	first, _ := ParseTarget(`getByRole('button').first()`)
	if ref, ok := resolveLocatorAX(sess, first.Calls); !ok || ref != "e2" {
		t.Errorf("first() = %q, %v; want e2, true", ref, ok)
	}
	last, _ := ParseTarget(`getByRole('button').last()`)
	if ref, ok := resolveLocatorAX(sess, last.Calls); !ok || ref != "e6" {
		t.Errorf("last() = %q, %v; want e6, true", ref, ok)
	}
	nth, _ := ParseTarget(`getByRole('button').nth(1)`)
	if ref, ok := resolveLocatorAX(sess, nth.Calls); !ok || ref != "e3" {
		t.Errorf("nth(1) = %q, %v; want e3, true", ref, ok)
	}
	// Sin coincidencia: fallback (found=false), no error.
	if _, ok := locatorAXRef(sess, `getByRole button "No existe"`); ok {
		t.Errorf("missing element should not resolve via AX")
	}
	// Métodos fuera de AX: fallback.
	if _, ok := locatorAXRef(sess, `getByTestId submit`); ok {
		t.Errorf("getByTestId should not resolve via AX")
	}
	// count exacto por AX.
	count, _ := ParseTarget(`getByRole('button')`)
	if n := len(resolveLocatorAXMatches(sess, count.Calls)); n != 3 {
		t.Errorf("count buttons = %d, want 3", n)
	}
}

func TestNameMatches(t *testing.T) {
	if !nameMatches("Enviar", "Enviar formulario", false) {
		t.Errorf("substring should match")
	}
	if nameMatches("Enviar", "Enviar formulario", true) {
		t.Errorf("exact should not match a longer name")
	}
	if !nameMatches("  ENVIAR  formulario ", "enviar Formulario", true) {
		t.Errorf("exact should match after normalising case and spaces")
	}
	if !nameMatches("", "cualquier cosa", false) {
		t.Errorf("empty want matches anything")
	}
}

func TestRoleMatches(t *testing.T) {
	if !roleMatches("textbox", "searchbox") || !roleMatches("button", "button") {
		t.Errorf("textbox/searchbox alias or equal roles should match")
	}
	if roleMatches("button", "link") {
		t.Errorf("different roles should not match")
	}
}
