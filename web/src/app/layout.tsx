import type { Metadata, Viewport } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Agent Atlas",
  description: "A2A agents on a world map, talking through agentgateway. Slow enough to follow.",
};

export const viewport: Viewport = { width: "device-width", initialScale: 1 };

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body className="flex h-screen flex-col overflow-hidden">{children}</body>
    </html>
  );
}
