import "@testing-library/jest-dom";

// jsdom does not implement scrollIntoView; the chat calls it on new turns.
// A no-op keeps component tests focused on what they assert.
if (typeof Element !== "undefined" && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}
