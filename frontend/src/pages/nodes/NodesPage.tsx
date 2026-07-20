import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Checkbox, Col, ConfigProvider, Input, Layout, Modal, Result, Row, Spin, Statistic, Tabs, Typography, message } from 'antd';
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  CloudServerOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';

import { useTheme } from '@/hooks/useTheme';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useNodesQuery } from '@/api/queries/useNodesQuery';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import { useNodeMutations } from '@/api/queries/useNodeMutations';
import { useManagedServersQuery } from '@/api/queries/useManagedServersQuery';
import type { ManagedServerRecord } from '@/api/queries/useManagedServersQuery';
import { useManagedServerMutations } from '@/api/queries/useManagedServerMutations';
import AppSidebar from '@/layouts/AppSidebar';
import NodeList from './NodeList';
import NodeFormModal from './NodeFormModal';
import ServerList from './ServerList';
import ServerFormModal from './ServerFormModal';
import BulkAddServersModal from './BulkAddServersModal';
import InstallPanelModal from './InstallPanelModal';
import ExecCommandModal from './ExecCommandModal';
import UploadFileModal from './UploadFileModal';
import ExecHistoryModal from './ExecHistoryModal';
import { setMessageInstance } from '@/utils/messageBus';
import { HttpUtil } from '@/utils';
import type { PanelUpdateInfo } from '../index/PanelUpdateModal';

// Confirm-dialog body that lets the operator pick the stable or dev channel for
// a node panel update. Reports changes via onChange so the imperative
// modal.confirm onOk can read the latest choice through a ref.
function UpdateChannelChoice({ onChange }: { onChange: (dev: boolean) => void }) {
  const { t } = useTranslation();
  const [dev, setDev] = useState(false);
  return (
    <div>
      <p>{t('pages.nodes.updateConfirmContent')}</p>
      <Checkbox
        checked={dev}
        onChange={(e) => { setDev(e.target.checked); onChange(e.target.checked); }}
      >
        {t('pages.nodes.updateDevChannel')}
      </Checkbox>
      {dev && (
        <Alert
          type="info"
          showIcon
          style={{ marginTop: 8 }}
          title={t('pages.index.devChannelWarning')}
        />
      )}
    </div>
  );
}

export default function NodesPage() {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const { isMobile } = useMediaQuery();
  const [modal, modalContextHolder] = Modal.useModal();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => { setMessageInstance(messageApi); }, [messageApi]);

  const { nodes, loading, fetched, fetchError, refetch, totals } = useNodesQuery();
  const { create, update, remove, setEnable, testConnection, fetchFingerprint, fetchInbounds, probe, updatePanels } = useNodeMutations();
  const { servers, loading: serversLoading } = useManagedServersQuery();
  const serverMutations = useManagedServerMutations();

  const { data: latestVersion = '' } = useQuery({
    queryKey: ['server', 'panelUpdateInfo'],
    queryFn: async () => {
      const msg = await HttpUtil.get<PanelUpdateInfo>('/panel/api/server/getPanelUpdateInfo');
      return msg?.obj?.latestVersion || '';
    },
    staleTime: 5 * 60 * 1000,
  });

  const [activeTab, setActiveTab] = useState<'servers' | 'nodes'>('nodes');
  const [formOpen, setFormOpen] = useState(false);
  const [formMode, setFormMode] = useState<'add' | 'edit'>('add');
  const [formNode, setFormNode] = useState<NodeRecord | null>(null);
  const [selectedIds, setSelectedIds] = useState<number[]>([]);
  const [serverFormOpen, setServerFormOpen] = useState(false);
  const [serverFormMode, setServerFormMode] = useState<'add' | 'edit'>('add');
  const [formServer, setFormServer] = useState<ManagedServerRecord | null>(null);
  const [selectedServerIds, setSelectedServerIds] = useState<number[]>([]);
  const [bulkAddOpen, setBulkAddOpen] = useState(false);
  const [installOpen, setInstallOpen] = useState(false);
  const [installTargets, setInstallTargets] = useState<ManagedServerRecord[]>([]);
  const [execOpen, setExecOpen] = useState(false);
  const [execTargets, setExecTargets] = useState<ManagedServerRecord[]>([]);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [uploadTargets, setUploadTargets] = useState<ManagedServerRecord[]>([]);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [mtlsOpen, setMtlsOpen] = useState(false);
  const [trustCa, setTrustCa] = useState('');
  const [copyingCa, setCopyingCa] = useState(false);
  const [savingTrustCa, setSavingTrustCa] = useState(false);

  // Cross-links between the two tabs: a server row shows the panel node it
  // derived, a node row shows the server it was installed from.
  const nodeNameById = useMemo(() => {
    const m = new Map<number, string>();
    for (const n of nodes) if (n.id > 0) m.set(n.id, n.name || `#${n.id}`);
    return m;
  }, [nodes]);

  // A panel node can be shared by several server rows (same box, different
  // names), so a node maps to the list of server names that reach it.
  const serverNamesByNodeId = useMemo(() => {
    const m = new Map<number, string[]>();
    for (const s of servers) {
      if (!s.nodeId) continue;
      const names = m.get(s.nodeId) ?? [];
      names.push(s.name || `#${s.id}`);
      m.set(s.nodeId, names);
    }
    return m;
  }, [servers]);

  const onCopyNodeCa = useCallback(async () => {
    setCopyingCa(true);
    try {
      const msg = await HttpUtil.post<{ caCert: string }>('/panel/api/nodes/mtls/ca');
      const ca = msg?.obj?.caCert;
      if (msg?.success && ca) {
        await navigator.clipboard.writeText(ca);
        messageApi.success(t('pages.nodes.mtls.caCopied'));
      } else {
        messageApi.error(msg?.msg || t('pages.nodes.mtls.caFailed'));
      }
    } catch {
      messageApi.error(t('pages.nodes.mtls.caFailed'));
    } finally {
      setCopyingCa(false);
    }
  }, [messageApi, t]);

  const onSaveTrustCa = useCallback(async () => {
    setSavingTrustCa(true);
    try {
      const msg = await HttpUtil.post('/panel/api/nodes/mtls/trustCA', { caCert: trustCa });
      if (msg?.success) {
        messageApi.success(t('pages.nodes.mtls.saved'));
        setMtlsOpen(false);
      } else {
        messageApi.error(msg?.msg || t('somethingWentWrong'));
      }
    } catch {
      messageApi.error(t('somethingWentWrong'));
    } finally {
      setSavingTrustCa(false);
    }
  }, [trustCa, messageApi, t]);

  const onAdd = useCallback(() => {
    setFormMode('add');
    setFormNode(null);
    setFormOpen(true);
  }, []);

  const onEdit = useCallback((node: NodeRecord) => {
    setFormMode('edit');
    setFormNode({ ...node });
    setFormOpen(true);
  }, []);

  const onSave = useCallback(async (payload: Partial<NodeRecord>) => {
    if (formMode === 'edit' && formNode?.id) {
      return update(formNode.id, payload);
    }
    return create(payload);
  }, [formMode, formNode, update, create]);

  const onDelete = useCallback((node: NodeRecord) => {
    modal.confirm({
      title: t('pages.nodes.deleteConfirmTitle', { name: node.name }),
      content: t('pages.nodes.deleteConfirmContent'),
      okText: t('delete'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: async () => {
        const msg = await remove(node.id);
        if (msg?.success) messageApi.success(t('pages.nodes.toasts.deleted'));
      },
    });
  }, [modal, t, remove, messageApi]);

  const onProbe = useCallback(async (node: NodeRecord) => {
    const msg = await probe(node.id);
    if (msg?.success && msg.obj) {
      if (msg.obj.status === 'online') {
        // Even if xray is in error/stop on the node we still reached its panel API.
        messageApi.success(t('pages.nodes.connectionOk', { ms: msg.obj.latencyMs }));
      } else {
        messageApi.error(msg.obj.error || t('pages.nodes.toasts.probeFailed'));
      }
    }
    // Refresh the list so the new xrayState / xrayError (if any) appears immediately in the row.
    refetch();
  }, [probe, t, messageApi, refetch]);

  const onToggleEnable = useCallback(async (node: NodeRecord, next: boolean) => {
    await setEnable(node.id, next);
  }, [setEnable]);

  const devRef = useRef(false);

  const runUpdate = useCallback(async (ids: number[], dev: boolean) => {
    const msg = await updatePanels(ids, dev);
    if (!msg?.success) {
      messageApi.error(msg?.msg || t('somethingWentWrong'));
      return;
    }
    const results = msg.obj ?? [];
    const ok = results.filter((r) => r.ok).length;
    const failed = results.length - ok;
    if (failed === 0) {
      messageApi.success(t('pages.nodes.toasts.updateStarted'));
    } else {
      const firstError = results.find((r) => !r.ok)?.error ?? '';
      const base = t('pages.nodes.toasts.updateResult', { ok, failed });
      messageApi.warning(firstError ? `${base} — ${firstError}` : base);
    }
    setSelectedIds([]);
  }, [updatePanels, messageApi, t]);

  const onUpdateNode = useCallback((node: NodeRecord) => {
    devRef.current = false;
    modal.confirm({
      title: t('pages.nodes.updateConfirmTitle', { count: 1 }),
      content: <UpdateChannelChoice onChange={(v) => { devRef.current = v; }} />,
      okText: t('update'),
      cancelText: t('cancel'),
      onOk: () => runUpdate([node.id], devRef.current),
    });
  }, [modal, t, runUpdate]);

  const onUpdateSelected = useCallback(() => {
    const eligible = nodes
      .filter((n) => selectedIds.includes(n.id) && n.enable && n.status === 'online')
      .map((n) => n.id);
    if (eligible.length === 0) {
      messageApi.warning(t('pages.nodes.toasts.updateNoneEligible'));
      return;
    }
    devRef.current = false;
    modal.confirm({
      title: t('pages.nodes.updateConfirmTitle', { count: eligible.length }),
      content: <UpdateChannelChoice onChange={(v) => { devRef.current = v; }} />,
      okText: t('update'),
      cancelText: t('cancel'),
      onOk: () => runUpdate(eligible, devRef.current),
    });
  }, [modal, t, nodes, selectedIds, runUpdate, messageApi]);

  const onAddServer = useCallback(() => {
    setServerFormMode('add');
    setFormServer(null);
    setServerFormOpen(true);
  }, []);

  const onEditServer = useCallback((server: ManagedServerRecord) => {
    setServerFormMode('edit');
    setFormServer({ ...server });
    setServerFormOpen(true);
  }, []);

  const onSaveServer = useCallback(async (payload: Partial<ManagedServerRecord>) => {
    if (serverFormMode === 'edit' && formServer?.id) {
      return serverMutations.update(formServer.id, payload);
    }
    return serverMutations.create(payload);
  }, [serverFormMode, formServer, serverMutations]);

  const onDeleteServer = useCallback((server: ManagedServerRecord) => {
    modal.confirm({
      title: t('pages.servers.deleteConfirmTitle', { name: server.name }),
      content: t('pages.servers.deleteConfirmContent'),
      okText: t('delete'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: async () => {
        const msg = await serverMutations.remove(server.id);
        if (msg?.success) messageApi.success(t('pages.nodes.toasts.deleted'));
      },
    });
  }, [modal, t, serverMutations, messageApi]);

  const onToggleServerEnable = useCallback(async (server: ManagedServerRecord, next: boolean) => {
    await serverMutations.setEnable(server.id, next);
  }, [serverMutations]);

  // The install modal drives both single and batch installs; targets carries
  // which servers it will act on and the version picker collects the version.
  const onInstall = useCallback((server: ManagedServerRecord) => {
    setInstallTargets([server]);
    setInstallOpen(true);
  }, []);

  const onBatchInstall = useCallback(() => {
    const targets = servers.filter((s) => selectedServerIds.includes(s.id) && !s.panelInstalled && !s.nodeId);
    if (targets.length === 0) return;
    setInstallTargets(targets);
    setInstallOpen(true);
  }, [servers, selectedServerIds]);

  // runInstall executes the actual install once the version is chosen. One
  // server shows a detailed derived/not-derived toast; a batch shows a summary.
  const runInstall = useCallback(async (version: string) => {
    if (installTargets.length === 1) {
      const server = installTargets[0];
      const hide = messageApi.loading(t('pages.nodes.install.running'), 0);
      try {
        const msg = await serverMutations.installPanel(server.id, version);
        hide();
        if (msg?.success && msg.obj?.success) {
          if (msg.obj.derived) messageApi.success(t('pages.nodes.install.derivedOk'));
          else messageApi.warning(msg.obj.message || t('pages.nodes.install.installedNotDerived'));
        } else {
          messageApi.error(msg?.obj?.message || msg?.msg || t('pages.nodes.install.failed'));
        }
      } catch {
        hide();
        messageApi.error(t('pages.nodes.install.failed'));
      }
      return;
    }
    const ids = installTargets.map((s) => s.id);
    const hide = messageApi.loading(t('pages.nodes.install.running'), 0);
    try {
      const msg = await serverMutations.installPanelBatch(ids, version);
      hide();
      const results = msg?.obj?.results ?? [];
      const ok = results.filter((r) => r.success).length;
      messageApi.open({ type: ok === results.length ? 'success' : 'warning', content: t('pages.servers.batchResult', { ok, failed: results.length - ok }) });
      setSelectedServerIds([]);
    } catch {
      hide();
      messageApi.error(t('pages.nodes.install.failed'));
    }
  }, [installTargets, serverMutations, messageApi, t]);

  const onImport = useCallback((server: ManagedServerRecord) => {
    modal.confirm({
      title: t('pages.servers.importConfirmTitle', { name: server.name || `#${server.id}` }),
      content: t('pages.servers.importConfirmBody'),
      okText: t('pages.servers.importAction'),
      cancelText: t('cancel'),
      onOk: async () => {
        const hide = messageApi.loading(t('pages.nodes.install.running'), 0);
        try {
          const msg = await serverMutations.importPanel(server.id);
          hide();
          if (msg?.success && msg.obj?.success) messageApi.success(t('pages.servers.importOk'));
          else messageApi.error(msg?.obj?.message || msg?.msg || t('pages.servers.importFailed'));
        } catch {
          hide();
          messageApi.error(t('pages.servers.importFailed'));
        }
      },
    });
  }, [modal, t, messageApi, serverMutations]);

  const onBatchImport = useCallback(() => {
    const targets = servers.filter((s) => selectedServerIds.includes(s.id) && s.panelInstalled && !s.nodeId);
    if (targets.length === 0) return;
    modal.confirm({
      title: t('pages.servers.batchImportConfirmTitle', { count: targets.length }),
      content: t('pages.servers.importConfirmBody'),
      okText: t('pages.servers.importAction'),
      cancelText: t('cancel'),
      onOk: async () => {
        const hide = messageApi.loading(t('pages.nodes.install.running'), 0);
        try {
          // Import is light (no install), so the servers are imported
          // concurrently client-side rather than through a batch endpoint.
          const results = await Promise.all(targets.map((s) => serverMutations.importPanel(s.id)));
          hide();
          const ok = results.filter((m) => m?.success && m.obj?.success).length;
          messageApi.open({ type: ok === results.length ? 'success' : 'warning', content: t('pages.servers.batchResult', { ok, failed: results.length - ok }) });
          setSelectedServerIds([]);
        } catch {
          hide();
          messageApi.error(t('pages.servers.importFailed'));
        }
      },
    });
  }, [modal, t, messageApi, serverMutations, servers, selectedServerIds]);

  const onUninstall = useCallback((server: ManagedServerRecord) => {
    modal.confirm({
      title: t('pages.servers.uninstallConfirmTitle', { name: server.name || `#${server.id}` }),
      content: t('pages.servers.uninstallConfirmBody'),
      okText: t('pages.servers.uninstallAction'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: async () => {
        const hide = messageApi.loading(t('pages.nodes.install.running'), 0);
        try {
          const msg = await serverMutations.uninstallPanel(server.id);
          hide();
          if (msg?.success && msg.obj?.success) messageApi.success(t('pages.servers.uninstallOk'));
          else messageApi.error(msg?.obj?.message || msg?.msg || t('pages.servers.uninstallFailed'));
        } catch {
          hide();
          messageApi.error(t('pages.servers.uninstallFailed'));
        }
      },
    });
  }, [modal, t, messageApi, serverMutations]);

  const onBatchUninstall = useCallback(() => {
    const targets = servers.filter((s) => selectedServerIds.includes(s.id) && (s.panelInstalled || s.nodeId));
    if (targets.length === 0) return;
    modal.confirm({
      title: t('pages.servers.batchUninstallConfirmTitle', { count: targets.length }),
      content: t('pages.servers.uninstallConfirmBody'),
      okText: t('pages.servers.uninstallAction'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: async () => {
        const hide = messageApi.loading(t('pages.nodes.install.running'), 0);
        try {
          const msg = await serverMutations.uninstallPanelBatch(targets.map((s) => s.id));
          hide();
          const results = msg?.obj?.results ?? [];
          const ok = results.filter((r) => r.success).length;
          messageApi.open({ type: ok === results.length ? 'success' : 'warning', content: t('pages.servers.batchResult', { ok, failed: results.length - ok }) });
          setSelectedServerIds([]);
        } catch {
          hide();
          messageApi.error(t('pages.servers.uninstallFailed'));
        }
      },
    });
  }, [modal, t, messageApi, serverMutations, servers, selectedServerIds]);

  const onBatchDelete = useCallback(() => {
    const targets = servers.filter((s) => selectedServerIds.includes(s.id));
    if (targets.length === 0) return;
    modal.confirm({
      title: t('pages.servers.batchDeleteConfirmTitle', { count: targets.length }),
      content: t('pages.servers.batchDeleteConfirmBody'),
      okText: t('delete'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: async () => {
        const msg = await serverMutations.removeBatch(targets.map((s) => s.id));
        if (msg?.success) messageApi.success(t('pages.nodes.toasts.deleted'));
        setSelectedServerIds([]);
      },
    });
  }, [modal, t, messageApi, serverMutations, servers, selectedServerIds]);

  const onExecSelected = useCallback(() => {
    const targets = servers.filter((s) => selectedServerIds.includes(s.id));
    if (targets.length === 0) return;
    setExecTargets(targets);
    setExecOpen(true);
  }, [servers, selectedServerIds]);

  const onUploadSelected = useCallback(() => {
    const targets = servers.filter((s) => selectedServerIds.includes(s.id));
    if (targets.length === 0) return;
    setUploadTargets(targets);
    setUploadOpen(true);
  }, [servers, selectedServerIds]);

  const onViewNode = useCallback(() => {
    setActiveTab('nodes');
  }, []);

  const pageClass = useMemo(() => {
    const classes = ['nodes-page'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);

  const nodesTab = (
    <Row gutter={[isMobile ? 8 : 16, isMobile ? 8 : 12]}>
      <Col span={24}>
        <Card size="small" hoverable className="summary-card">
          <Row gutter={[16, isMobile ? 16 : 12]}>
            <Col xs={12} sm={12} md={6}>
              <Statistic
                title={t('pages.nodes.totalNodes')}
                value={String(totals.total)}
                prefix={<CloudServerOutlined />}
              />
            </Col>
            <Col xs={12} sm={12} md={6}>
              <Statistic
                title={t('pages.nodes.onlineNodes')}
                value={String(totals.online)}
                prefix={<CheckCircleOutlined style={{ color: 'var(--ant-color-success)' }} />}
              />
            </Col>
            <Col xs={12} sm={12} md={6}>
              <Statistic
                title={t('pages.nodes.offlineNodes')}
                value={String(totals.offline)}
                prefix={<CloseCircleOutlined style={{ color: 'var(--ant-color-error)' }} />}
              />
            </Col>
            <Col xs={12} sm={12} md={6}>
              <Statistic
                title={t('pages.nodes.avgLatency')}
                value={totals.avgLatency > 0 ? `${totals.avgLatency} ms` : '-'}
                prefix={<ThunderboltOutlined />}
              />
            </Col>
          </Row>
        </Card>
      </Col>

      <Col span={24}>
        <NodeList
          nodes={nodes}
          loading={loading}
          isMobile={isMobile}
          latestVersion={latestVersion}
          serverNamesByNodeId={serverNamesByNodeId}
          selectedIds={selectedIds}
          onSelectionChange={setSelectedIds}
          onAdd={onAdd}
          onMtls={() => setMtlsOpen(true)}
          onEdit={onEdit}
          onDelete={onDelete}
          onProbe={onProbe}
          onToggleEnable={onToggleEnable}
          onUpdateNode={onUpdateNode}
          onUpdateSelected={onUpdateSelected}
        />
      </Col>
    </Row>
  );

  const serversTab = (
    <Row gutter={[isMobile ? 8 : 16, isMobile ? 8 : 12]}>
      <Col span={24}>
        <ServerList
          servers={servers}
          loading={serversLoading}
          nodeNameById={nodeNameById}
          selectedIds={selectedServerIds}
          onSelectionChange={setSelectedServerIds}
          onAdd={onAddServer}
          onBulkAdd={() => setBulkAddOpen(true)}
          onEdit={onEditServer}
          onDelete={onDeleteServer}
          onToggleEnable={onToggleServerEnable}
          onInstall={onInstall}
          onImport={onImport}
          onUninstall={onUninstall}
          onViewNode={onViewNode}
          onExecSelected={onExecSelected}
          onUploadSelected={onUploadSelected}
          onBatchInstall={onBatchInstall}
          onBatchImport={onBatchImport}
          onBatchUninstall={onBatchUninstall}
          onBatchDelete={onBatchDelete}
          onExecHistory={() => setHistoryOpen(true)}
        />
      </Col>
    </Row>
  );

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      {modalContextHolder}
      <Layout className={pageClass}>
        <AppSidebar />

        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <Spin spinning={!fetched} delay={200} description={t('loading')} size="large">
              {!fetched ? (
                <div className="loading-spacer" />
              ) : fetchError ? (
                <Result
                  status="error"
                  title={t('somethingWentWrong')}
                  subTitle={fetchError}
                  extra={<Button type="primary" loading={loading} onClick={() => refetch()}>{t('refresh')}</Button>}
                />
              ) : (
                <Tabs
                  activeKey={activeTab}
                  onChange={(key) => setActiveTab(key as 'servers' | 'nodes')}
                  items={[
                    { key: 'nodes', label: t('pages.nodes.tab'), children: nodesTab },
                    { key: 'servers', label: t('pages.servers.tab'), children: serversTab },
                  ]}
                />
              )}
            </Spin>
          </Layout.Content>
        </Layout>

        <NodeFormModal
          open={formOpen}
          mode={formMode}
          node={formNode}
          testConnection={testConnection}
          fetchFingerprint={fetchFingerprint}
          fetchInbounds={fetchInbounds}
          save={onSave}
          onOpenChange={setFormOpen}
        />

        <ServerFormModal
          open={serverFormOpen}
          mode={serverFormMode}
          server={formServer}
          testSSH={serverMutations.testSSH}
          save={onSaveServer}
          onOpenChange={setServerFormOpen}
        />

        <BulkAddServersModal
          open={bulkAddOpen}
          createBatch={serverMutations.createBatch}
          onOpenChange={setBulkAddOpen}
        />

        <InstallPanelModal
          open={installOpen}
          targets={installTargets}
          fetchVersions={serverMutations.fetchPanelVersions}
          onConfirm={runInstall}
          onOpenChange={setInstallOpen}
        />

        <ExecCommandModal
          open={execOpen}
          targets={execTargets}
          execCommand={serverMutations.execCommand}
          onOpenChange={setExecOpen}
        />

        <UploadFileModal
          open={uploadOpen}
          targets={uploadTargets}
          uploadFile={serverMutations.uploadFile}
          onOpenChange={setUploadOpen}
        />

        <ExecHistoryModal
          open={historyOpen}
          fetchHistory={serverMutations.fetchExecHistory}
          onOpenChange={setHistoryOpen}
        />

        <Modal
          open={mtlsOpen}
          title={t('pages.nodes.mtls.title')}
          footer={null}
          onCancel={() => setMtlsOpen(false)}
          destroyOnHidden
        >
          <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
            {t('pages.nodes.mtls.intro')}
          </Typography.Paragraph>
          <Button onClick={onCopyNodeCa} loading={copyingCa} style={{ marginBottom: 4 }}>
            {t('pages.nodes.mtls.copyCa')}
          </Button>
          <Typography.Paragraph type="secondary">
            {t('pages.nodes.mtls.copyCaHint')}
          </Typography.Paragraph>
          <Typography.Text strong>{t('pages.nodes.mtls.trustLabel')}</Typography.Text>
          <Input.TextArea
            rows={5}
            value={trustCa}
            onChange={(e) => setTrustCa(e.target.value)}
            placeholder={t('pages.nodes.mtls.trustPlaceholder')}
            style={{ marginTop: 4, fontFamily: 'monospace' }}
          />
          <Typography.Paragraph type="secondary" style={{ marginTop: 4 }}>
            {t('pages.nodes.mtls.trustHint')}
          </Typography.Paragraph>
          <Button type="primary" onClick={onSaveTrustCa} loading={savingTrustCa} block>
            {t('pages.nodes.mtls.save')}
          </Button>
        </Modal>
      </Layout>
    </ConfigProvider>
  );
}
