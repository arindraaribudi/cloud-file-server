import { useMutation } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { PasswordForm } from "../components/PasswordForm";
import { resetAdminPassword } from "../lib/api";

export function AdminPassword() {
  const { username } = useParams({ from: "/_authed/admins/$username/password" });
  const nav = useNavigate();
  const mutation = useMutation({
    mutationFn: (password: string) => resetAdminPassword(username, password),
    onSuccess: () => nav({ to: "/admins" }),
  });

  return (
    <PasswordForm
      eyebrow="Reset admin password"
      title={"Reset password for " + username}
      submitting={mutation.isPending}
      error={mutation.error ? String(mutation.error) : null}
      success={mutation.isSuccess ? "Password reset." : null}
      onSubmit={({ password }) => mutation.mutate(password)}
      onCancel={() => nav({ to: "/admins" })}
    />
  );
}
