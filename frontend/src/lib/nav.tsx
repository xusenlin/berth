import { createContext, useContext } from "react";

export interface Route {
  server?: string;
  app?: string;
  tab?: string;
}

export const NavContext = createContext<{ route: Route; go: (r: Route) => void }>({ route: {}, go: () => {} });

export function useNav() {
  return useContext(NavContext);
}
