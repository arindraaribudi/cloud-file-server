import { createRootRoute, createRoute, createRouter, Outlet, redirect } from "@tanstack/react-router";
import { Login } from "./routes/login";
import { AuthCallback } from "./routes/auth-callback";
import { AuthedLayout } from "./routes/_authed";
import { Users } from "./routes/users";
import { UserNew } from "./routes/user-new";
import { UserEdit } from "./routes/user-edit";
import { UserPassword } from "./routes/user-password";
import { Audit } from "./routes/audit";
import { Files } from "./routes/files";

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
      auditRoute,
      filesRoute,
    ]),
  ]),
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
