export interface StreamKeyHasher {
  hash(plaintext: string): Promise<string>;
  verify(plaintext: string, encoded: string): Promise<boolean>;
}
