import { createRootRoute, createRoute, createRouter, Outlet, redirect } from "@tanstack/react-router";
import { Login } from "./routes/login";
import { AuthCallback } from "./routes/auth-callback";
import { AuthedLayout } from "./routes/_authed";
import { Users } from "./routes/users";
import { UserNew } from "./routes/user-new";
import { UserEdit } from "./routes/user-edit";
import { UserPassword } from "./routes/user-password";
import { UserSFTPKey } from "./routes/user-sftp-key";
import { Audit } from "./routes/audit";
import { Files } from "./routes/files";
import { Workflows } from "./routes/workflows";
import { WorkflowNew, WorkflowEdit } from "./routes/workflow-editor";
import { WorkflowRuns, WorkflowRunDetail } from "./routes/workflow-runs";
import { Admins } from "./routes/admins";
import { AdminNew } from "./routes/admin-new";
import { AdminPassword } from "./routes/admin-password";
import type { Session } from "./lib/api";

const rootRoute = createRootRoute({
  component: () => <Outlet />,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: Login,
});

const authCallbackRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/auth/callback",
  component: AuthCallback,
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/login" });
  },
});

const authedRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "_authed",
  beforeLoad: async () => {
    let response: Response;
    try {
      response = await fetch("/api/v1/auth/session", { credentials: "include" });
    } catch {
      throw redirect({ to: "/login" });
    }
    if (!response.ok) {
      throw redirect({ to: "/login" });
    }
    try {
      return { session: (await response.json()) as Session };
    } catch {
      throw redirect({ to: "/login" });
    }
  },
  component: AuthedLayout,
});

const usersRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/users",
  component: Users,
});

const userNewRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/users/new",
  component: UserNew,
});

const userEditRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/users/$username/edit",
  component: UserEdit,
});

const userPasswordRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/users/$username/password",
  component: UserPassword,
});

const userSFTPKeyRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/users/$username/sftp-key",
  component: UserSFTPKey,
});

const auditRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/audit",
  component: Audit,
});

const filesRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/files/$userId",
  component: Files,
  validateSearch: (search: Record<string, unknown>): { path?: string } => ({
    path: typeof search.path === "string" ? search.path : undefined,
  }),
});

const workflowsRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/workflows",
  component: Workflows,
});

const workflowNewRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/workflows/new",
  component: WorkflowNew,
});

const workflowEditRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/workflows/$id/edit",
  component: WorkflowEdit,
});

const workflowRunsRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/workflows/$id/runs",
  component: WorkflowRuns,
});

const workflowRunDetailRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/workflows/$id/runs/$runId",
  component: WorkflowRunDetail,
});

const adminsRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/admins",
  component: Admins,
});

const adminNewRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/admins/new",
  component: AdminNew,
});

const adminPasswordRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/admins/$username/password",
  component: AdminPassword,
});

export const router = createRouter({
  routeTree: rootRoute.addChildren([
    indexRoute,
    loginRoute,
    authCallbackRoute,
    authedRoute.addChildren([
      usersRoute,
      userNewRoute,
      userEditRoute,
      userPasswordRoute,
      userSFTPKeyRoute,
      auditRoute,
      filesRoute,
      workflowsRoute,
      workflowNewRoute,
      workflowEditRoute,
      workflowRunsRoute,
      workflowRunDetailRoute,
      adminsRoute,
      adminNewRoute,
      adminPasswordRoute,
    ]),
  ]),
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
