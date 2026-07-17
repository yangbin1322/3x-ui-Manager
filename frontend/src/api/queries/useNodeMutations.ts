import { useMutation, useQueryClient } from '@tanstack/react-query';

import { HttpUtil, Msg } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import { ProbeResultSchema, type ProbeResult } from '@/schemas/node';
import type { SSHTestResult, BatchExecResult, ExecHistoryResponse, InstallResult } from '@/generated/types';

export interface ExecHistoryFilter {
  page?: number;
  pageSize?: number;
  nodeId?: number;
  username?: string;
  status?: string;
}

export type { ProbeResult };

export interface NodeUpdateResult {
  id: number;
  name?: string;
  ok: boolean;
  error?: string;
}

export interface RemoteInboundOption {
  tag: string;
  remark?: string;
  protocol?: string;
  port?: number;
}

export function useNodeMutations() {
  const queryClient = useQueryClient();
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: keys.nodes.root() });
    queryClient.invalidateQueries({ queryKey: keys.inbounds.options() });
  };

  const createMut = useMutation({
    mutationFn: (payload: Partial<NodeRecord>) =>
      HttpUtil.post('/panel/api/nodes/add', payload),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const updateMut = useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: Partial<NodeRecord> }) =>
      HttpUtil.post(`/panel/api/nodes/update/${id}`, payload),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const removeMut = useMutation({
    mutationFn: (id: number) =>
      HttpUtil.post(`/panel/api/nodes/del/${id}`),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const setEnableMut = useMutation({
    mutationFn: ({ id, enable }: { id: number; enable: boolean }) =>
      HttpUtil.post(`/panel/api/nodes/setEnable/${id}`, { enable }),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const probeMut = useMutation({
    mutationFn: async (id: number): Promise<Msg<ProbeResult>> => {
      const raw = await HttpUtil.post(`/panel/api/nodes/probe/${id}`);
      return parseMsg(raw, ProbeResultSchema, 'nodes/probe');
    },
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const installMut = useMutation({
    mutationFn: ({ nodeId, version }: { nodeId: number; version: string }) =>
      HttpUtil.post<InstallResult>('/panel/api/nodes/install', { nodeId, version }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const updatePanelsMut = useMutation({
    mutationFn: ({ ids, dev }: { ids: number[]; dev: boolean }) =>
      HttpUtil.post<NodeUpdateResult[]>('/panel/api/nodes/updatePanel', { ids, dev }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  return {
    create: (payload: Partial<NodeRecord>) => createMut.mutateAsync(payload),
    update: (id: number, payload: Partial<NodeRecord>) => updateMut.mutateAsync({ id, payload }),
    remove: (id: number) => removeMut.mutateAsync(id),
    setEnable: (id: number, enable: boolean) => setEnableMut.mutateAsync({ id, enable }),
    probe: (id: number) => probeMut.mutateAsync(id),
    updatePanels: (ids: number[], dev: boolean): Promise<Msg<NodeUpdateResult[]>> => updatePanelsMut.mutateAsync({ ids, dev }),
    testConnection: async (payload: Partial<NodeRecord>): Promise<Msg<ProbeResult>> => {
      const raw = await HttpUtil.post('/panel/api/nodes/test', payload);
      return parseMsg(raw, ProbeResultSchema, 'nodes/test');
    },
    testSSH: (payload: Partial<NodeRecord>, id?: number): Promise<Msg<SSHTestResult>> =>
      HttpUtil.post<SSHTestResult>(
        id ? `/panel/api/nodes/testSSH?id=${id}` : '/panel/api/nodes/testSSH',
        payload,
      ),
    execCommand: (nodeIds: number[], command: string, timeoutSec: number): Promise<Msg<BatchExecResult>> =>
      HttpUtil.post<BatchExecResult>('/panel/api/nodes/exec', { nodeIds, command, timeoutSec }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    installPanel: (nodeId: number, version: string): Promise<Msg<InstallResult>> => installMut.mutateAsync({ nodeId, version }),
    fetchExecHistory: (filter: ExecHistoryFilter): Promise<Msg<ExecHistoryResponse>> => {
      const q = new URLSearchParams();
      if (filter.page) q.set('page', String(filter.page));
      if (filter.pageSize) q.set('pageSize', String(filter.pageSize));
      if (filter.nodeId) q.set('nodeId', String(filter.nodeId));
      if (filter.username) q.set('username', filter.username);
      if (filter.status) q.set('status', filter.status);
      const qs = q.toString();
      return HttpUtil.get<ExecHistoryResponse>(`/panel/api/nodes/execHistory${qs ? `?${qs}` : ''}`);
    },
    fetchFingerprint: (payload: Partial<NodeRecord>): Promise<Msg<string>> =>
      HttpUtil.post<string>('/panel/api/nodes/certFingerprint', payload),
    fetchInbounds: (payload: Partial<NodeRecord>): Promise<Msg<RemoteInboundOption[]>> =>
      HttpUtil.post<RemoteInboundOption[]>('/panel/api/nodes/inbounds', payload),
  };
}
