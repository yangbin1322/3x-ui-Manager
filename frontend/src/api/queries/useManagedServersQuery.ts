import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { ManagedServerListSchema } from '@/schemas/managedServer';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import { keys } from '@/api/queryKeys';

export type { ManagedServerRecord };

async function fetchManagedServers(): Promise<ManagedServerRecord[]> {
  const msg = await HttpUtil.get('/panel/api/managedServers/list', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch servers');
  const validated = parseMsg(msg, ManagedServerListSchema, 'managedServers/list');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

export function useManagedServersQuery() {
  const query = useQuery({
    queryKey: keys.managedServers.list(),
    queryFn: fetchManagedServers,
    refetchInterval: 15_000,
  });

  const servers = useMemo(() => query.data ?? [], [query.data]);

  return {
    servers,
    loading: query.isFetching,
    fetched: query.data !== undefined || query.isError,
    fetchError: query.error ? (query.error as Error).message : '',
    refetch: query.refetch,
  };
}
