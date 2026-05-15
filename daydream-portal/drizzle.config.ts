import type { Config } from "drizzle-kit";

const config: Config = {
  schema: "./src/db/schema.ts",
  out: "./migrations",
  dialect: "postgresql",
  dbCredentials: {
    url: process.env.DAYDREAM_PORTAL_POSTGRES_URL ?? "",
  },
  strict: true,
  verbose: true,
};

export default config;
