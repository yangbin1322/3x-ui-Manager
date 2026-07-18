import { useMutation, useQueryClient } from '@tanstack/react-query';

import { HttpUtil, Msg } from '@/utils';
import { keys } from '@/api/queryKeys';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import type { SSHTestResult, BatchExecResult, ExecHistoryResponse, InstallResult } from '@/generated/types';

export interface ExecHistoryFilter {
  page?: number;
  pageSize?: number;
  serverId?: number;
  username?: string;
  status?: string;
}

export function useManagedServerMutations() {
  const queryClient = useQueryClient();
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: keys.managedServers.root() });
  };

  const createMut = useMutation({
    mutationFn: (payload: Partial<ManagedServerRecord>) =>
      HttpUtil.post('/panel/api/managedServers/add', payload),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const updateMut = useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: Partial<ManagedServerRecord> }) =>
      HttpUtil.post(`/panel/api/managedServers/update/${id}`, payload),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const removeMut = useMutation({
    mutationFn: (id: number) =>
      HttpUtil.post(`/panel/api/managedServers/del/${id}`),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const setEnableMut = useMutation({
    mutationFn: ({ id, enable }: { id: number; enable: boolean }) =>
      HttpUtil.post(`/panel/api/managedServers/setEnable/${id}`, { enable }),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const installMut = useMutation({
    mutationFn: ({ serverId, version }: { serverId: number; version: string }) =>
      HttpUtil.post<InstallResult>('/panel/api/managedServers/install', { serverId, version }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: (msg) => {
      if (msg?.success) {
        invalidate();
        queryClient.invalidateQueries({ queryKey: keys.nodes.root() });
      }
    },
  });

  return {
    create: (payload: Partial<ManagedServerRecord>) => createMut.mutateAsync(payload),
    update: (id: number, payload: Partial<ManagedServerRecord>) => updateMut.mutateAsync({ id, payload }),
    remove: (id: number) => removeMut.mutateAsync(id),
    setEnable: (id: number, enable: boolean) => setEnableMut.mutateAsync({ id, enable }),
    testSSH: (payload: Partial<ManagedServerRecord>, id?: number): Promise<Msg<SSHTestResult>> =>
      HttpUtil.post<SSHTestResult>(
        id ? `/panel/api/managedServers/test?id=${id}` : '/panel/api/managedServers/test',
        payload,
      ),
    execCommand: (serverIds: number[], command: string, timeoutSec: number): Promise<Msg<BatchExecResult>> =>
      HttpUtil.post<BatchExecResult>('/panel/api/managedServers/exec', { serverIds, command, timeoutSec }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    installPanel: (serverId: number, version: string): Promise<Msg<InstallResult>> =>
      installMut.mutateAsync({ serverId, version }),
    fetchExecHistory: (filter: ExecHistoryFilter): Promise<Msg<ExecHistoryResponse>> => {
      const q = new URLSearchParams();
      if (filter.page) q.set('page', String(filter.page));
      if (filter.pageSize) q.set('pageSize', String(filter.pageSize));
      if (filter.serverId) q.set('serverId', String(filter.serverId));
      if (filter.username) q.set('username', filter.username);
      if (filter.status) q.set('status', filter.status);
      const qs = q.toString();
      return HttpUtil.get<ExecHistoryResponse>(`/panel/api/managedServers/execHistory${qs ? `?${qs}` : ''}`);
    },
  };
}
