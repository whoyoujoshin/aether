import type { ReactNode } from "react";
import { HeadContent, Outlet, Scripts, createRootRoute } from "@tanstack/react-router";
import { AuthProvider } from "@/lib/auth/provider";
import { AppErrorComponent } from "@/lib/error-component";
import { PreviewHostBridge } from "@/components/preview-host-bridge";
import appCss from "@/styles.css?url";

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1" },
      { title: "Aether Miner" },
      {
        name: "description",
        content: "Set up a recovery phrase, connect to the Aether testnet, and mine with this computer's CPU.",
      },
    ],
    links: [
      { rel: "icon", href: "/aether-mark.svg", type: "image/svg+xml" },
      { rel: "stylesheet", href: appCss },
      {
        rel: "stylesheet",
        href: "https://fonts.googleapis.com/css2?family=Fraunces:opsz,wght@9..144,500;9..144,600&family=Outfit:wght@400;500;600&display=swap",
      },
    ],
  }),
  component: RootComponent,
  errorComponent: AppErrorComponent,
});

function RootComponent() {
  return (
    <RootDocument>
      <AuthProvider>
        <PreviewHostBridge />
        <Outlet />
      </AuthProvider>
    </RootDocument>
  );
}

function RootDocument({ children }: Readonly<{ children: ReactNode }>) {
  return (
    <html lang="en">
      <head>
        <HeadContent />
      </head>
      <body>
        {children}
        <Scripts />
      </body>
    </html>
  );
}
