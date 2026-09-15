type StampTone = "blue" | "red";

export function Stamp({
  label,
  tone,
  size = "sm",
}: {
  label: string;
  tone: StampTone;
  size?: "sm" | "lg";
}) {
  return <span className={`stamp stamp--${tone} stamp--${size}`}>{label}</span>;
}
