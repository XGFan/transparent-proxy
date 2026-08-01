import { createContext } from 'preact';
import { useContext } from 'preact/hooks';
import type { ApiClient } from './client';

export const ApiContext = createContext<ApiClient | null>(null);

export function useApi(): ApiClient {
  const client = useContext(ApiContext);
  if (!client) {
    throw new Error('useApi must be used inside <ApiProvider>');
  }
  return client;
}
