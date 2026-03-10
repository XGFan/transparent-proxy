import { defineConfig } from 'eslint/config';
import tseslint from '@typescript-eslint/eslint-plugin/use-at-your-own-risk/raw-plugin';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';

const tsFiles = ['**/*.{ts,tsx}'];

const tsRecommended = tseslint.flatConfigs['flat/recommended'].map((config) => ({
  ...config,
  files: config.files ?? tsFiles,
}));

export default defineConfig([
  {
    ignores: ['dist', 'coverage', '**/*.{js,mjs,cjs}'],
  },
  ...tsRecommended,
  {
    ...reactHooks.configs['recommended-latest'],
    files: tsFiles,
  },
  {
    ...reactRefresh.configs.vite,
    files: ['**/*.tsx'],
  },
]);
