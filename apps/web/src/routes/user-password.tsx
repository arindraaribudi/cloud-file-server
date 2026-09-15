import { useMutation } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { resetUserPassword } from "../lib/api";
import { PasswordForm } from "../components/PasswordForm";

export function UserPassword() {
  const { username } = useParams({ from: "/_authed/users/$username/password" });
  const nav = useNavigate();

  const mutation = useMutation({
    mutationFn: (password: string) => resetUserPassword(username, password),
  });

  return (
    <PasswordForm
      eyebrow="Reset password"
      title={`Reset password for ${username}`}
      submitting={mutation.isPending}
      error={mutation.error ? String(mutation.error) : null}
      success={mutation.isSuccess ? "Password reset. Notify the user directly." : null}
      onSubmit={({ password }) => mutation.mutate(password)}
      onCancel={() => nav({ to: "/users" })}
    />
  );
}
