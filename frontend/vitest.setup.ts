import "@testing-library/jest-dom";

// jsdom does not implement scrollIntoView; the chat calls it on new turns.
// A no-op keeps component tests focused on what they assert.
if (typeof Element !== "undefined" && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

// jsdom does not implement matchMedia; the IDE's responsive layout reads
// (min-width: 768px). Report a desktop-width match with no-op listeners so
// layout effects settle without a real viewport.
if (typeof window !== "undefined" && !window.matchMedia) {
  window.matchMedia = (query: string): MediaQueryList =>
    ({
      matches: true,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList;
}
