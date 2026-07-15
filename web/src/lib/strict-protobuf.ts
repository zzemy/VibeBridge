/** Rejects forward fields on security-sensitive handshake messages. */
export function assertNoUnknownFields(value: unknown) {
  const seen = new WeakSet<object>();
  visit(value, seen);
}

function visit(value: unknown, seen: WeakSet<object>) {
  if (typeof value !== "object" || value === null || ArrayBuffer.isView(value) || value instanceof ArrayBuffer) return;
  if (seen.has(value)) return;
  seen.add(value);
  if (Array.isArray(value)) {
    for (const item of value) visit(item, seen);
    return;
  }
  for (const [key, field] of Object.entries(value)) {
    if (key === "$unknown" && Array.isArray(field) && field.length > 0) {
      throw new Error("Protocol message contains unknown fields");
    }
    visit(field, seen);
  }
}
