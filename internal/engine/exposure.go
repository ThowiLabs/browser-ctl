package engine

import (
	"fmt"
	"strings"
)

// hiddenSchemes are browser/extension page schemes agents never get
// (mirrors the extension's exposure rules).
var hiddenSchemes = map[string]bool{
	"about:": true, "arc:": true, "brave:": true, "chrome:": true,
	"chrome-error:": true, "chrome-extension:": true, "chrome-search:": true,
	"chrome-untrusted:": true, "devtools:": true, "edge:": true,
	"extension:": true, "moz-extension:": true, "opera:": true,
	"vivaldi:": true, "view-source:": true,
}

var storeURLs = []string{
	"https://chromewebstore.google.com/",
	"https://chrome.google.com/webstore",
	"https://microsoftedge.microsoft.com/addons",
	"https://addons.opera.com/",
}

// IsExposedURL reports whether agents may use a page at this URL.
func IsExposedURL(url string, fileAccess bool) bool {
	if url == "" {
		return false
	}
	if url == "about:blank" {
		return true
	}
	scheme := ""
	if i := strings.Index(url, ":"); i > 0 {
		scheme = url[:i+1]
	}
	if scheme == "file:" {
		return fileAccess
	}
	if hiddenSchemes[scheme] {
		return false
	}
	for _, store := range storeURLs {
		if strings.HasPrefix(url, store) {
			return false
		}
	}
	return true
}

// RequireExposedURL returns a clear error when the agent tries to open a
// browser page.
func RequireExposedURL(url string) error {
	if !IsExposedURL(url, false) {
		return fmt.Errorf("agents can't open %s: it's a browser page", url)
	}
	return nil
}
