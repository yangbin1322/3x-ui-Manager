import { useMutation, useQueryClient } from '@tanstack/react-query';

import { HttpUtil, Msg } from '@/utils';
import { uploadWithProgress } from '@/api/http-init';
import { keys } from '@/api/queryKeys';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import type { SSHTestResult, BatchExecResult, BatchUploadResult, BatchCopyResult, ExecHistoryResponse, InstallResult, UninstallResult, BatchInstallResponse, BulkAddResponse } from '@/generated/types';

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
    // The backend probes a just-added / just-changed server in the background,
    // so its reachability and panel state land a moment after the mutation
    // returns. Re-fetch a few times over the next several seconds to pull that
    // in without making the user wait for the 15s heartbeat tick.
    [1500, 3500, 6000].forEach((ms) => {
      setTimeout(() => queryClient.invalidateQueries({ queryKey: keys.managedServers.list() }), ms);
    });
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

  const createBatchMut = useMutation({
    mutationFn: ({ servers, verify }: { servers: Partial<ManagedServerRecord>[]; verify: boolean }) =>
      HttpUtil.post<BulkAddResponse>('/panel/api/managedServers/addBatch', { servers, verify }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const removeMut = useMutation({
    mutationFn: (id: number) =>
      HttpUtil.post(`/panel/api/managedServers/del/${id}`),
    onSuccess: (msg) => {
      if (msg?.success) {
        invalidate();
        queryClient.invalidateQueries({ queryKey: keys.nodes.root() });
      }
    },
  });

  const removeBatchMut = useMutation({
    mutationFn: (serverIds: number[]) =>
      HttpUtil.post<{ removed: number }>('/panel/api/managedServers/delBatch', { serverIds }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: (msg) => {
      if (msg?.success) {
        invalidate();
        queryClient.invalidateQueries({ queryKey: keys.nodes.root() });
      }
    },
  });

  const setEnableMut = useMutation({
    mutationFn: ({ id, enable }: { id: number; enable: boolean }) =>
      HttpUtil.post(`/panel/api/managedServers/setEnable/${id}`, { enable }),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  // Install, import, uninstall and their batch forms all reshape both the
  // server list (panel state, linked node) and the node list (derived nodes),
  // so they share one invalidator.
  const invalidateBoth = (msg: { success?: boolean } | undefined) => {
    if (msg?.success) {
      invalidate();
      queryClient.invalidateQueries({ queryKey: keys.nodes.root() });
    }
  };

  const installMut = useMutation({
    mutationFn: ({ serverId, version }: { serverId: number; version: string }) =>
      HttpUtil.post<InstallResult>('/panel/api/managedServers/install', { serverId, version }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: invalidateBoth,
  });

  const importMut = useMutation({
    mutationFn: (serverId: number) =>
      HttpUtil.post<InstallResult>('/panel/api/managedServers/import', { serverId }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: invalidateBoth,
  });

  const uninstallMut = useMutation({
    mutationFn: (serverId: number) =>
      HttpUtil.post<UninstallResult>('/panel/api/managedServers/uninstall', { serverId }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: invalidateBoth,
  });

  const installBatchMut = useMutation({
    mutationFn: ({ serverIds, version }: { serverIds: number[]; version: string }) =>
      HttpUtil.post<BatchInstallResponse>('/panel/api/managedServers/installBatch', { serverIds, version }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: invalidateBoth,
  });

  const uninstallBatchMut = useMutation({
    mutationFn: (serverIds: number[]) =>
      HttpUtil.post<BatchInstallResponse>('/panel/api/managedServers/uninstallBatch', { serverIds }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: invalidateBoth,
  });

  return {
    create: (payload: Partial<ManagedServerRecord>) => createMut.mutateAsync(payload),
    createBatch: (servers: Partial<ManagedServerRecord>[], verify: boolean): Promise<Msg<BulkAddResponse>> => createBatchMut.mutateAsync({ servers, verify }),
    update: (id: number, payload: Partial<ManagedServerRecord>) => updateMut.mutateAsync({ id, payload }),
    remove: (id: number) => removeMut.mutateAsync(id),
    removeBatch: (serverIds: number[]): Promise<Msg<{ removed: number }>> => removeBatchMut.mutateAsync(serverIds),
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
    uploadFile: async (
      serverIds: number[],
      files: File[],
      dest: string,
      timeoutSec: number,
      onProgress?: (fraction: number) => void,
    ): Promise<Msg<BatchUploadResult>> => {
      const form = new FormData();
      for (const file of files) {
        form.append('file', file);
        const rel = (file as File & { webkitRelativePath?: string }).webkitRelativePath || '';
        form.append('path', rel);
      }
      form.append('serverIds', serverIds.join(','));
      form.append('dest', dest);
      form.append('timeoutSec', String(timeoutSec));
      const raw = await uploadWithProgress('/panel/api/managedServers/upload', form, onProgress);
      const env = (raw ?? {}) as { success?: boolean; msg?: string; obj?: BatchUploadResult };
      return new Msg<BatchUploadResult>(Boolean(env.success), typeof env.msg === 'string' ? env.msg : '', env.obj ?? null);
    },
    copyPath: (sourceId: number, sourcePath: string, targetIds: number[], dest: string, timeoutSec: number): Promise<Msg<BatchCopyResult>> =>
      HttpUtil.post<BatchCopyResult>('/panel/api/managedServers/copyPath', { sourceId, sourcePath, targetIds, dest, timeoutSec }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    installPanel: (serverId: number, version: string): Promise<Msg<InstallResult>> =>
      installMut.mutateAsync({ serverId, version }),
    importPanel: (serverId: number): Promise<Msg<InstallResult>> => importMut.mutateAsync(serverId),
    uninstallPanel: (serverId: number): Promise<Msg<UninstallResult>> => uninstallMut.mutateAsync(serverId),
    installPanelBatch: (serverIds: number[], version: string): Promise<Msg<BatchInstallResponse>> =>
      installBatchMut.mutateAsync({ serverIds, version }),
    uninstallPanelBatch: (serverIds: number[]): Promise<Msg<BatchInstallResponse>> =>
      uninstallBatchMut.mutateAsync(serverIds),
    fetchPanelVersions: (): Promise<Msg<string[]>> =>
      HttpUtil.get<string[]>('/panel/api/managedServers/panelVersions'),
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
