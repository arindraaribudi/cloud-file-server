import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { createUser } from "../lib/api";
import { UserForm } from "../components/UserForm";

export function UserNew() {
  const nav = useNavigate();
  const qc = useQueryClient();

  const mutation = useMutation({
    mutationFn: createUser,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      nav({ to: "/users" });
    },
  });

  return (
    <UserForm
      mode="create"
      submitting={mutation.isPending}
      error={mutation.error ? String(mutation.error) : null}
      onSubmit={(values) => mutation.mutate(values)}
      onCancel={() => nav({ to: "/users" })}
    />
  );
}
