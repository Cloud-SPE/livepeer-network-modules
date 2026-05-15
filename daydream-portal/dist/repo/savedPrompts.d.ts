import type { NodePgDatabase } from "drizzle-orm/node-postgres";
export type PromptDb = NodePgDatabase<Record<string, unknown>>;
export interface SavedPromptRow {
    id: string;
    customerId: string;
    label: string;
    body: string;
    createdAt: Date;
    updatedAt: Date;
}
export declare function listForCustomer(db: PromptDb, customerId: string): Promise<SavedPromptRow[]>;
export declare function createPrompt(db: PromptDb, input: {
    customerId: string;
    label: string;
    body: string;
}): Promise<SavedPromptRow>;
export declare function updatePrompt(db: PromptDb, input: {
    id: string;
    customerId: string;
    label?: string;
    body?: string;
}): Promise<SavedPromptRow | null>;
export declare function deletePrompt(db: PromptDb, input: {
    id: string;
    customerId: string;
}): Promise<boolean>;
