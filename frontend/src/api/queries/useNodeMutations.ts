import { useMutation, useQueryClient } from '@tanstack/react-query';

import { HttpUtil, Msg } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import type { NodeBatchResponse } from '@/generated/types';
import { ProbeResultSchema, type ProbeResult } from '@/schemas/node';

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

  const updatePanelsMut = useMutation({
    mutationFn: ({ ids, dev }: { ids: number[]; dev: boolean }) =>
      HttpUtil.post<NodeUpdateResult[]>('/panel/api/nodes/updatePanel', { ids, dev }, {
        headers: { 'Content-Type': 'application/json' },
      }),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const jsonHeaders = { headers: { 'Content-Type': 'application/json' } };

  const bulkDelMut = useMutation({
    mutationFn: ({ ids, force }: { ids: number[]; force: boolean }) =>
      HttpUtil.post<NodeBatchResponse>('/panel/api/nodes/bulkDel', { ids, force }, jsonHeaders),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const removeInboundsMut = useMutation({
    mutationFn: (ids: number[]) =>
      HttpUtil.post<NodeBatchResponse>('/panel/api/nodes/removeInbounds', { ids }, jsonHeaders),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const removeClientsMut = useMutation({
    mutationFn: (ids: number[]) =>
      HttpUtil.post<NodeBatchResponse>('/panel/api/nodes/removeClients', { ids }, jsonHeaders),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  return {
    create: (payload: Partial<NodeRecord>) => createMut.mutateAsync(payload),
    update: (id: number, payload: Partial<NodeRecord>) => updateMut.mutateAsync({ id, payload }),
    remove: (id: number) => removeMut.mutateAsync(id),
    setEnable: (id: number, enable: boolean) => setEnableMut.mutateAsync({ id, enable }),
    probe: (id: number) => probeMut.mutateAsync(id),
    updatePanels: (ids: number[], dev: boolean): Promise<Msg<NodeUpdateResult[]>> => updatePanelsMut.mutateAsync({ ids, dev }),
    bulkDelNodes: (ids: number[], force: boolean): Promise<Msg<NodeBatchResponse>> => bulkDelMut.mutateAsync({ ids, force }),
    removeNodeInbounds: (ids: number[]): Promise<Msg<NodeBatchResponse>> => removeInboundsMut.mutateAsync(ids),
    removeNodeClients: (ids: number[]): Promise<Msg<NodeBatchResponse>> => removeClientsMut.mutateAsync(ids),
    testConnection: async (payload: Partial<NodeRecord>): Promise<Msg<ProbeResult>> => {
      const raw = await HttpUtil.post('/panel/api/nodes/test', payload);
      return parseMsg(raw, ProbeResultSchema, 'nodes/test');
    },
    fetchFingerprint: (payload: Partial<NodeRecord>): Promise<Msg<string>> =>
      HttpUtil.post<string>('/panel/api/nodes/certFingerprint', payload),
    fetchInbounds: (payload: Partial<NodeRecord>): Promise<Msg<RemoteInboundOption[]>> =>
      HttpUtil.post<RemoteInboundOption[]>('/panel/api/nodes/inbounds', payload),
  };
}
