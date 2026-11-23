import { browser } from "wxt/browser";

import { REMOVE_FRAMES_MESSAGE, removeExtensionFrames } from "./frames";

/**
 * For a content script declared in the extension's manifest (`matches: ["<all_urls>"]`, `allFrames: true`). It does nothing until the background asks, when another extension's frame (a password manager's inline menu) blocks `debugger.attach()`: then it removes those frames from its document, including from closed shadow roots, which only a manifest content script can look into (`chrome.dom`). Pages and other extensions can't send it messages
 */
export function handleFrameRemovalRequests(): void {
  browser.runtime.onMessage.addListener((message, sender, sendResponse) => {
    if (
      sender.id === browser.runtime.id &&
      (message as { type?: unknown } | null)?.type === REMOVE_FRAMES_MESSAGE
    ) {
      sendResponse(removeExtensionFrames(browser.runtime.id));
    }
  });
}
/** The message the background sends the content script with the cursor overlay's position */
export const CURSOR_MESSAGE = "browser-ctl:cursor";

/** How long the cursor overlay stays after the last position before it removes itself from the page */
const CURSOR_IDLE_MS = 1500;

/**
 * Draws the agent's cursor in the page, in this content script's isolated
 * world: the overlay is a bare host div with a random id per page load,
 * and everything visible lives in a closed shadow root, so the page sees
 * no id to match, no text in innerText and no node to query. The position
 * comes from the background (one message per step of the engine's path);
 * each position is applied on the next animation frame, so the steps
 * follow the page's own frame cadence instead of a fixed timer. The host
 * removes itself when idle, so a page that looks later finds nothing.
 * Same look as the JS original: big blue SVG arrow with glow + pill label.
 */
export function handleCursorMessages(): void {
  // Only the main frame draws: the background targets frameId 0, and
  // this guards against a message meant for another frame.
  if (window.top !== window.self) {
    return;
  }
  let host: HTMLDivElement | null = null;
  let hideTimer: ReturnType<typeof setTimeout> | undefined;
  let queued: { x: number; y: number } | null = null;
  let frameQueued = false;

  const remove = () => {
    host?.remove();
    host = null;
    queued = null;
  };

  const applyQueued = () => {
    frameQueued = false;
    if (!queued || !host) {
      return;
    }
    host.style.transform = `translate(${queued.x}px, ${queued.y}px)`;
  };

  const ensureHost = () => {
    if (host) {
      return host;
    }
    const id =
      "bctl-" +
      [...globalThis.crypto.getRandomValues(new Uint8Array(8))]
        .map((n) => n.toString(16).padStart(2, "0"))
        .join("");
    host = document.createElement("div");
    host.id = id;
    host.setAttribute("aria-hidden", "true");
    host.style.cssText =
      "position:fixed;left:0;top:0;width:0;height:0;z-index:2147483647;pointer-events:none;";
    host.attachShadow({ mode: "closed" }).innerHTML =
      '<style>.arrow{position:absolute;left:-3px;top:-3px;filter:drop-shadow(0 2px 3px rgba(0,0,0,.45)) drop-shadow(0 0 6px rgba(37,99,235,.9));}.label{position:absolute;left:22px;top:26px;padding:3px 9px;border-radius:999px;background:#2563eb;color:#fff;font:600 12px/16px system-ui,-apple-system,sans-serif;white-space:nowrap;border:1.5px solid #fff;box-shadow:0 2px 6px rgba(0,0,0,.35);}</style><svg class="arrow" width="30" height="36" viewBox="0 0 20 24"><path d="M1 1 L1 19 L6 14.5 L9.5 22 L12.5 20.7 L9 13.3 L15.5 13 Z" fill="#2563eb" stroke="#fff" stroke-width="1.6" stroke-linejoin="round"/></svg><div class="label">browser-ctl</div>';
    document.documentElement.appendChild(host);
    return host;
  };

  browser.runtime.onMessage.addListener((message, sender) => {
    if (
      sender.id !== browser.runtime.id ||
      (message as { type?: unknown } | null)?.type !== CURSOR_MESSAGE
    ) {
      return;
    }
    const { x, y, visible } = message as {
      x: number;
      y: number;
      visible: boolean;
    };
    if (!visible || !document.documentElement) {
      clearTimeout(hideTimer);
      remove();
      return;
    }
    ensureHost();
    queued = { x, y };
    if (!frameQueued) {
      frameQueued = true;
      requestAnimationFrame(applyQueued);
    }
    clearTimeout(hideTimer);
    hideTimer = setTimeout(remove, CURSOR_IDLE_MS);
  });
}
