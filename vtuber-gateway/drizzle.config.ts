import type { Config } from "drizzle-kit";

const config: Config = {
  schema: "./src/repo/schema.ts",
  out: "./migrations",
  dialect: "postgresql",
  dbCredentials: {
    url: process.env["DATABASE_URL"] ?? "postgres://localhost/vtuber_gateway",
  },
  schemaFilter: ["vtuber"],
} satisfies Config;

export default config;
