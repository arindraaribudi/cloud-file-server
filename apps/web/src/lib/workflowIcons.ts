// Tiny inline SVG icons so each step is recognisable at a glance.

function svg(bg: string, body: string): string {
  const markup =
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">' +
    '<rect width="24" height="24" rx="5" fill="' + bg + '"/>' +
    '<g fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
    body +
    "</g></svg>";
  return "data:image/svg+xml;utf8," + encodeURIComponent(markup);
}

// Keys are the designer-safe step types (component keys without punctuation).
export const STEP_ICONS: Record<string, string> = {
  triggerfileupload: svg("#2f6f9f", '<path d="M13 4 7 13h4l-1 7 6-9h-4z"/>'),
  triggerfileremove: svg("#2f6f9f", '<path d="M13 4 7 13h4l-1 7 6-9h-4z"/>'),
  processfilerename: svg("#6b7785", '<path d="M6 18l1-4 8-8 3 3-8 8z"/>'),
  processfileremove: svg("#6b7785", '<path d="M7 8h10M10 8V6h4v2M8 8l1 10h6l1-10"/>'),
  processcondition: svg("#a97400", '<path d="M12 5l7 7-7 7-7-7z"/>'),
  conditionboxcollapsed: svg("#a97400", '<path d="M8 10l4 4 4-4"/><path d="M6 17h12"/>'),
  conditionbox: svg("#a97400", '<path d="M12 5l7 7-7 7-7-7z"/>'),
  outputfilecopy: svg("#4f7d52", '<rect x="9" y="9" width="9" height="9" rx="1.5"/><path d="M6 15V7a1 1 0 0 1 1-1h8"/>'),
  outputnotifywebhook: svg("#3a6fb0", '<path d="M7 17 17 7M9 7h8v8"/>'),
  outputnotifyemail: svg("#7a56a5", '<rect x="5" y="7" width="14" height="10" rx="1.5"/><path d="m5 8 7 5 7-5"/>'),
};
