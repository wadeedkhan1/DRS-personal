// Copying text is not as simple as navigator.clipboard.writeText. That API only exists in a
// secure context, and this panel is routinely reached over plain HTTP on a LAN address
// (http://192.168.x.x:3000) where `navigator.clipboard` is undefined outright — calling into
// it threw a TypeError and every copy button in the UI silently did nothing. Even where the
// API exists, writeText rejects when the document is not focused or permission is denied.
//
// So: try the modern API, and fall back to the old execCommand('copy') trick, which has no
// secure-context requirement. Returns whether the text actually made it to the clipboard, so
// the caller can tell the user when it did not instead of flashing a false confirmation.
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Fall through to the legacy path rather than give up.
    }
  }
  return legacyCopy(text);
}

function legacyCopy(text: string): boolean {
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    // Keep it off-screen but still selectable — display:none or visibility:hidden would make
    // the selection empty and the copy a no-op. readOnly stops the mobile keyboard popping up.
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.top = '0';
    ta.style.left = '-9999px';
    document.body.appendChild(ta);

    const previous = document.activeElement as HTMLElement | null;
    ta.select();
    ta.setSelectionRange(0, text.length);
    const ok = document.execCommand('copy');

    document.body.removeChild(ta);
    previous?.focus?.();
    return ok;
  } catch {
    return false;
  }
}
