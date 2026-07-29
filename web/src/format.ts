const relativeTime = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
const absoluteTime = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

const reactionSymbols: Readonly<Record<string, string>> = {
  "+1": "👍",
  "-1": "👎",
  confused: "😕",
  eyes: "👀",
  heart: "❤️",
  hooray: "🎉",
  laugh: "😄",
  rocket: "🚀",
};

export function formatRelativeTime(value: string | null, referenceTime = Date.now()): string {
  if (!value) {
    return "never";
  }

  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) {
    return "unknown";
  }

  const elapsedSeconds = Math.round((timestamp - referenceTime) / 1_000);
  if (Math.abs(elapsedSeconds) < 60) {
    return relativeTime.format(elapsedSeconds, "second");
  }

  const elapsedMinutes = Math.round(elapsedSeconds / 60);
  if (Math.abs(elapsedMinutes) < 60) {
    return relativeTime.format(elapsedMinutes, "minute");
  }

  const elapsedHours = Math.round(elapsedMinutes / 60);
  if (Math.abs(elapsedHours) < 24) {
    return relativeTime.format(elapsedHours, "hour");
  }

  return relativeTime.format(Math.round(elapsedHours / 24), "day");
}

export function formatAbsoluteTime(value: string | null): string {
  if (!value) {
    return "No timestamp";
  }

  const timestamp = Date.parse(value);
  return Number.isNaN(timestamp) ? "Unknown timestamp" : absoluteTime.format(timestamp);
}

export function reactionSymbol(content: string): string {
  return reactionSymbols[content] ?? content;
}
