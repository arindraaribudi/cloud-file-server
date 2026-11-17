import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { AdminForm } from "../components/AdminForm";
import { createAdmin } from "../lib/api";

export function AdminNew() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const mutation = useMutation({
    mutationFn: createAdmin,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admins"] });
      nav({ to: "/admins" });
    },
  });

  return (
    <AdminForm
      submitting={mutation.isPending}
      error={mutation.error ? String(mutation.error) : null}
      onSubmit={(values) => mutation.mutate(values)}
      onCancel={() => nav({ to: "/admins" })}
    />
  );
}
