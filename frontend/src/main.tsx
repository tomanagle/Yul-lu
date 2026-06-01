import React from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
// Devtools are disabled (JSX commented out below). Re-add these imports to
// re-enable: ReactQueryDevtools (@tanstack/react-query-devtools),
// TanStackRouterDevtools (@tanstack/router-devtools).

import { router } from "./router";
import "./index.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30 * 1000,
      retry: 1,
      // Background interval polling already keeps data fresh, so don't ALSO
      // refetch every query whenever the window regains focus — that turned
      // every alt-tab / devtools click into a burst of requests.
      refetchOnWindowFocus: false,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      {/* Devtools render nothing in production builds. */}
      {/* <ReactQueryDevtools initialIsOpen={false} buttonPosition="bottom-left" /> */}
      {/* <TanStackRouterDevtools router={router} position="bottom-right" /> */}
    </QueryClientProvider>
  </React.StrictMode>,
);
