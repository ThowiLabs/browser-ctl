import { defineContentScript } from "wxt/utils/define-content-script";

import {
  handleCursorMessages,
  handleFrameRemovalRequests,
} from "@browsermcp/extension-core/content";

/**
 * Idle until the background asks: remove another extension's frames, which block `debugger.attach()` (it runs in every frame so it can look into closed shadow roots, see `handleFrameRemovalRequests()`), or draw the agent's cursor (see `handleCursorMessages()`).
 */
export default defineContentScript({
  matches: ["<all_urls>"],
  allFrames: true,
  runAt: "document_start",
  main: () => {
    handleFrameRemovalRequests();
    handleCursorMessages();
  },
});
