import { createFileRoute } from "@tanstack/react-router";
import { MinerApp } from "@/miner/MinerApp";

export const Route = createFileRoute("/")({
  component: MinerApp,
});
