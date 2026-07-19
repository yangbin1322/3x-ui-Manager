import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Badge,
  Button,
  Card,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import type { BadgeProps } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ClusterOutlined,
  CodeOutlined,
  DeleteOutlined,
  DeploymentUnitOutlined,
  EditOutlined,
  ExclamationCircleOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
  HistoryOutlined,
  ImportOutlined,
  LinkOutlined,
  MinusCircleOutlined,
  PlusOutlined,
} from '@ant-design/icons';

import type { ManagedServerRecord } from '@/schemas/managedServer';
import { activateOnKey } from '@/utils/a11y';
import './NodeList.css';

interface ServerListProps {
  servers: ManagedServerRecord[];
  loading?: boolean;
  nodeNameById: Map<number, string>;
  selectedIds: number[];
  onSelectionChange: (ids: number[]) => void;
  onAdd: () => void;
  onEdit: (server: ManagedServerRecord) => void;
  onDelete: (server: ManagedServerRecord) => void;
  onToggleEnable: (server: ManagedServerRecord, next: boolean) => void;
  onInstall: (server: ManagedServerRecord) => void;
  onImport: (server: ManagedServerRecord) => void;
  onUninstall: (server: ManagedServerRecord) => void;
  onViewNode: (nodeId: number) => void;
  onExecSelected: () => void;
  onBatchInstall: () => void;
  onBatchImport: () => void;
  onBatchUninstall: () => void;
  onExecHistory: () => void;
}

function badgeStatus(status?: string): BadgeProps['status'] {
  switch (status) {
    case 'reachable': return 'success';
    case 'unreachable': return 'error';
    default: return 'default';
  }
}

function useRelativeTime() {
  const { t } = useTranslation();
  return (unixSeconds?: number) => {
    if (!unixSeconds) return t('pages.nodes.never');
    const diffSec = Math.max(0, Math.floor(Date.now() / 1000 - unixSeconds));
    if (diffSec < 5) return t('pages.nodes.justNow');
    if (diffSec < 60) return `${diffSec}s`;
    if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m`;
    if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h`;
    return `${Math.floor(diffSec / 86400)}d`;
  };
}

export default function ServerList({
  servers,
  loading = false,
  nodeNameById,
  selectedIds,
  onSelectionChange,
  onAdd,
  onEdit,
  onDelete,
  onToggleEnable,
  onInstall,
  onImport,
  onUninstall,
  onViewNode,
  onExecSelected,
  onBatchInstall,
  onBatchImport,
  onBatchUninstall,
  onExecHistory,
}: ServerListProps) {
  const { t } = useTranslation();
  const relativeTime = useRelativeTime();
  const [showAddress, setShowAddress] = useState(false);

  const columns = useMemo<ColumnsType<ManagedServerRecord>>(() => [
    {
      title: t('pages.nodes.actions'),
      align: 'center',
      width: 200,
      render: (_value, record) => (
        <Space>
          {!record.panelInstalled && !record.nodeId && (
            <Tooltip title={t('pages.nodes.install.action')}>
              <Button type="text" size="small" style={{ fontSize: 16 }} icon={<DeploymentUnitOutlined />} aria-label={t('pages.nodes.install.action')} onClick={() => onInstall(record)} />
            </Tooltip>
          )}
          {record.panelInstalled && !record.nodeId && (
            <Tooltip title={t('pages.servers.importAction')}>
              <Button type="text" size="small" style={{ fontSize: 16 }} icon={<ImportOutlined />} aria-label={t('pages.servers.importAction')} onClick={() => onImport(record)} />
            </Tooltip>
          )}
          {(record.panelInstalled || !!record.nodeId) && (
            <Tooltip title={t('pages.servers.uninstallAction')}>
              <Button type="text" size="small" danger style={{ fontSize: 16 }} icon={<MinusCircleOutlined />} aria-label={t('pages.servers.uninstallAction')} onClick={() => onUninstall(record)} />
            </Tooltip>
          )}
          <Tooltip title={t('edit')}>
            <Button type="text" size="small" style={{ fontSize: 16 }} icon={<EditOutlined />} aria-label={t('edit')} onClick={() => onEdit(record)} />
          </Tooltip>
          <Tooltip title={t('pages.servers.deleteConfirmTitle', { name: record.name || '' })}>
            <Button type="text" size="small" danger style={{ fontSize: 16 }} icon={<DeleteOutlined />} aria-label={t('delete')} onClick={() => onDelete(record)} />
          </Tooltip>
        </Space>
      ),
    },
    {
      title: t('pages.nodes.enable'),
      dataIndex: 'enable',
      align: 'center',
      width: 80,
      render: (_value, record) => (
        <Switch
          checked={!!record.enable}
          size="small"
          onChange={(v) => onToggleEnable(record, v)}
        />
      ),
    },
    {
      title: t('pages.nodes.name'),
      dataIndex: 'name',
      ellipsis: true,
      render: (_value, record) => (
        <div className="name-cell">
          <span className="name">{record.name}</span>
          {record.remark && <span className="remark">{record.remark}</span>}
        </div>
      ),
    },
    {
      title: (
        <span className="address-header">
          {t('pages.nodes.address')}
          <Tooltip title={t('pages.index.toggleIpVisibility')}>
            {showAddress ? (
              <EyeOutlined className="ip-toggle-icon" role="button" tabIndex={0} aria-label={t('pages.index.toggleIpVisibility')} onClick={() => setShowAddress(false)} onKeyDown={activateOnKey(() => setShowAddress(false))} />
            ) : (
              <EyeInvisibleOutlined className="ip-toggle-icon" role="button" tabIndex={0} aria-label={t('pages.index.toggleIpVisibility')} onClick={() => setShowAddress(true)} onKeyDown={activateOnKey(() => setShowAddress(true))} />
            )}
          </Tooltip>
        </span>
      ),
      dataIndex: 'address',
      ellipsis: true,
      render: (_value, record) => (
        <span className={showAddress ? 'address-visible' : 'address-hidden'}>
          {record.sshUser ? `${record.sshUser}@` : ''}{record.address}:{record.sshPort || 22}
        </span>
      ),
    },
    {
      title: t('pages.servers.os'),
      dataIndex: 'osName',
      align: 'center',
      render: (_value, record) =>
        record.osName ? `${record.osName} ${record.osVersion || ''}`.trim() : '-',
    },
    {
      title: t('pages.servers.versionColumn'),
      dataIndex: 'panelVersion',
      align: 'center',
      render: (_value, record) => record.panelInstalled ? (
        <Tag color="green" style={{ margin: 0 }}>{record.panelVersion || '3x-ui'}</Tag>
      ) : (
        <span style={{ opacity: 0.5 }}>{t('pages.servers.notInstalled')}</span>
      ),
    },
    {
      title: t('pages.nodes.status'),
      dataIndex: 'status',
      align: 'center',
      render: (_value, record) => (
        <Space size={4}>
          <Badge status={badgeStatus(record.status)} />
          <span>{t(`pages.nodes.statusValues.${record.status || 'unknown'}`)}</span>
          {record.lastError && (
            <Tooltip title={record.lastError}>
              <ExclamationCircleOutlined style={{ color: 'var(--ant-color-warning)' }} />
            </Tooltip>
          )}
        </Space>
      ),
    },
    {
      title: t('pages.servers.linkedNode'),
      dataIndex: 'nodeId',
      align: 'center',
      render: (_value, record) => record.nodeId ? (
        <Tooltip title={t('pages.servers.viewNode')}>
          <Tag
            color="blue"
            icon={<LinkOutlined />}
            style={{ margin: 0, cursor: 'pointer' }}
            role="button"
            tabIndex={0}
            onClick={() => onViewNode(record.nodeId!)}
            onKeyDown={activateOnKey(() => onViewNode(record.nodeId!))}
          >
            {nodeNameById.get(record.nodeId) || `#${record.nodeId}`}
          </Tag>
        </Tooltip>
      ) : (
        <span style={{ opacity: 0.4 }}>—</span>
      ),
    },
    {
      title: t('pages.nodes.latency'),
      dataIndex: 'latencyMs',
      align: 'center',
      width: 100,
      render: (_value, record) =>
        record.latencyMs && record.latencyMs > 0 ? `${record.latencyMs} ms` : '-',
    },
    {
      title: t('pages.nodes.lastHeartbeat'),
      dataIndex: 'lastHeartbeat',
      align: 'center',
      width: 120,
      render: (_value, record) => relativeTime(record.lastHeartbeat),
    },
  ], [t, showAddress, relativeTime, nodeNameById, onToggleEnable, onEdit, onDelete, onInstall, onImport, onUninstall, onViewNode]);

  // Batch buttons target the applicable subset of the selection: install a
  // server with no panel yet, import one that has a panel but no node, uninstall
  // one that has either. The buttons are always shown and disabled when the
  // selection has no applicable server (rather than hidden), so the toolbar
  // layout is stable and the available actions are discoverable.
  const selectedServers = useMemo(
    () => servers.filter((s) => selectedIds.includes(s.id)),
    [servers, selectedIds],
  );
  const installableCount = selectedServers.filter((s) => !s.panelInstalled && !s.nodeId).length;
  const importableCount = selectedServers.filter((s) => s.panelInstalled && !s.nodeId).length;
  const uninstallableCount = selectedServers.filter((s) => s.panelInstalled || s.nodeId).length;

  return (
    <Card size="small" hoverable>
      <div className="toolbar">
        <Button type="primary" icon={<PlusOutlined />} onClick={onAdd}>
          {t('pages.servers.addServer')}
        </Button>
        <Button icon={<HistoryOutlined />} onClick={onExecHistory}>
          {t('pages.nodes.exec.history.action')}
        </Button>
        <Button icon={<CodeOutlined />} disabled={selectedIds.length === 0} onClick={onExecSelected}>
          {t('pages.nodes.exec.action')}
        </Button>
        <Button icon={<DeploymentUnitOutlined />} disabled={installableCount === 0} onClick={onBatchInstall}>
          {t('pages.servers.batchInstall', { count: installableCount })}
        </Button>
        <Button icon={<ImportOutlined />} disabled={importableCount === 0} onClick={onBatchImport}>
          {t('pages.servers.batchImport', { count: importableCount })}
        </Button>
        <Button danger icon={<MinusCircleOutlined />} disabled={uninstallableCount === 0} onClick={onBatchUninstall}>
          {t('pages.servers.batchUninstall', { count: uninstallableCount })}
        </Button>
      </div>

      <Table<ManagedServerRecord>
        dataSource={servers}
        columns={columns}
        pagination={false}
        loading={loading}
        scroll={{ x: 'max-content' }}
        size="middle"
        rowKey="id"
        rowSelection={{
          selectedRowKeys: selectedIds,
          onChange: (keys) => onSelectionChange(keys.filter((k) => typeof k === 'number') as number[]),
          getCheckboxProps: (record) => ({ disabled: !record.enable }),
        }}
        locale={{
          emptyText: (
            <div className="card-empty">
              <ClusterOutlined style={{ fontSize: 32, marginBottom: 8 }} />
              <div>{t('noData')}</div>
            </div>
          ),
        }}
      />
    </Card>
  );
}
