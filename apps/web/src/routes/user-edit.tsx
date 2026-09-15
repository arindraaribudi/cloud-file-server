import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { api, updateUser, type FTPUser } from "../lib/api";
import { UserForm } from "../components/UserForm";

export function UserEdit() {
  const { username } = useParams({ from: "/_authed/users/$username/edit" });
  const nav = useNavigate();
  const qc = useQueryClient();

  const q = useQuery({
    queryKey: ["users"],
    queryFn: () => api<FTPUser[]>("/api/v1/users"),
  });
  const user = q.data?.find((u) => u.username === username);

  const mutation = useMutation({
    mutationFn: (values: { root_folder: string; enabled: boolean }) => updateUser(username, values),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      nav({ to: "/users" });
    },
  });

  if (q.isPending) return <p>loading...</p>;
  if (!user) return <p className="err">User not found.</p>;

  return (
    <UserForm
      mode="edit"
      initial={user}
      submitting={mutation.isPending}
      error={mutation.error ? String(mutation.error) : null}
      onSubmit={(values) => mutation.mutate({ root_folder: values.root_folder, enabled: values.enabled })}
      onCancel={() => nav({ to: "/users" })}
    />
  );
}
