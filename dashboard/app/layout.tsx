import type { Metadata } from "next";
import "./globals.css";
import HealthBar from "@/components/HealthBar";

export const metadata: Metadata = {
  title: "Calgary Transit Monitor",
  description:
    "Live delay and disruption monitor for Calgary Transit, built on the city's GTFS-Realtime feeds.",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>
        <div className="topbar">
          <span className="title">
            <a href="/">CALGARY-TRANSIT-MONITOR</a>
          </span>
          <span className="sub">
            GTFS-RT service alerts · polled every 30s from data.calgary.ca
          </span>
          <HealthBar />
        </div>
        <div className="wrap">{children}</div>
      </body>
    </html>
  );
}
