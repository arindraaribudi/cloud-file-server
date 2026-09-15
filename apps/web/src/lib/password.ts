// Mirrors apps/ftp/internal/auth.ValidatePassword — keep both in sync.
export function validatePassword(pw: string): string | null {
  if (pw.length < 8 || !/[A-Z]/.test(pw) || !/[a-z]/.test(pw) || !/[0-9]/.test(pw)) {
    return "Password must be at least 8 characters and include an uppercase letter, a lowercase letter, and a digit.";
  }
  return null;
}

const UPPER = "ABCDEFGHJKLMNPQRSTUVWXYZ";
const LOWER = "abcdefghijkmnpqrstuvwxyz";
const DIGIT = "23456789";
const ALL = UPPER + LOWER + DIGIT;

function randomChar(charset: string): string {
  const bytes = new Uint32Array(1);
  crypto.getRandomValues(bytes);
  return charset[bytes[0]! % charset.length]!;
}

export function generatePassword(length = 16): string {
  const chars = [randomChar(UPPER), randomChar(LOWER), randomChar(DIGIT)];
  for (let i = chars.length; i < length; i++) chars.push(randomChar(ALL));
  // Shuffle so the guaranteed classes aren't always in the first 3 positions.
  for (let i = chars.length - 1; i > 0; i--) {
    const bytes = new Uint32Array(1);
    crypto.getRandomValues(bytes);
    const j = bytes[0]! % (i + 1);
    [chars[i], chars[j]] = [chars[j]!, chars[i]!];
  }
  return chars.join("");
}
