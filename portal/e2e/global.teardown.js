import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

export default async () => {
  const artifactDir = process.env.TP_PLAYWRIGHT_ARTIFACT_DIR
    ? path.resolve(process.env.TP_PLAYWRIGHT_ARTIFACT_DIR)
    : path.resolve(__dirname, '..', 'test-results');

  fs.mkdirSync(artifactDir, { recursive: true });

  const payload = {
    finishedAt: new Date().toISOString(),
    suite: process.env.TP_TEST_SUITE_TIER || '',
  };

  fs.writeFileSync(
    path.join(artifactDir, 'harness-global-teardown.json'),
    JSON.stringify(payload, null, 2),
    'utf8',
  );
};
