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
  LinkOutlined,
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
  onViewNode: (nodeId: number) => void;
  onExecSelected: () => void;
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
  onViewNode,
  onExecSelected,
  onExecHistory,
}: ServerListProps) {
  const { t } = useTranslation();
  const relativeTime = useRelativeTime();
  const [showAddress, setShowAddress] = useState(false);

  const columns = useMemo<ColumnsType<ManagedServerRecord>>(() => [
    {
      title: t('pages.nodes.actions'),
      align: 'center',
      width: 170,
      render: (_value, record) => (
        <Space>
          {!record.nodeId && (
            <Tooltip title={t('pages.nodes.install.action')}>
              <Button type="text" size="small" style={{ fontSize: 16 }} icon={<DeploymentUnitOutlined />} aria-label={t('pages.nodes.install.action')} onClick={() => onInstall(record)} />
            </Tooltip>
          )}
          <Tooltip title={t('edit')}>
            <Button type="text" size="small" style={{ fontSize: 16 }} icon={<EditOutlined />} aria-label={t('edit')} onClick={() => onEdit(record)} />
          </Tooltip>
          <Tooltip title={t('delete')}>
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
  ], [t, showAddress, relativeTime, nodeNameById, onToggleEnable, onEdit, onDelete, onInstall, onViewNode]);

  return (
    <Card size="small" hoverable>
      <div className="toolbar">
        <Button type="primary" icon={<PlusOutlined />} onClick={onAdd}>
          {t('pages.servers.addServer')}
        </Button>
        <Button icon={<HistoryOutlined />} onClick={onExecHistory}>
          {t('pages.nodes.exec.history.action')}
        </Button>
        {selectedIds.length > 0 && (
          <Button icon={<CodeOutlined />} onClick={onExecSelected}>
            {t('pages.nodes.exec.action')}
          </Button>
        )}
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
