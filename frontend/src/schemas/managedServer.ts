import { z } from 'zod';

export const ManagedServerRecordSchema = z.object({
  id: z.number(),
  name: z.string().optional(),
  remark: z.string().optional(),
  address: z.string().optional(),
  enable: z.boolean().optional(),
  allowPrivateAddress: z.boolean().optional(),
  sshPort: z.number().optional(),
  sshUser: z.string().optional(),
  sshAuthType: z.enum(['password', 'key']).optional(),
  sshHostKeyMode: z.enum(['pin', 'trust', 'skip']).optional(),
  sshHostKeySha256: z.string().optional(),
  sshPasswordSet: z.boolean().optional(),
  sshPrivateKeySet: z.boolean().optional(),
  osName: z.string().optional(),
  osVersion: z.string().optional(),
  nodeId: z.number().optional(),
  status: z.string().optional(),
  lastHeartbeat: z.number().optional(),
  latencyMs: z.number().optional(),
  lastError: z.string().optional(),
}).loose();

export const ManagedServerListSchema = z.array(ManagedServerRecordSchema);

export const ManagedServerFormSchema = z.object({
  id: z.number().optional(),
  name: z.string().trim().min(1, 'pages.nodes.toasts.fillRequired'),
  remark: z.string().optional(),
  address: z.string().trim().min(1, 'pages.nodes.toasts.fillRequired'),
  enable: z.boolean(),
  allowPrivateAddress: z.boolean(),
  sshPort: z.number().int().min(1).max(65535).default(22),
  sshUser: z.string().trim().min(1, 'pages.nodes.toasts.fillRequired').default('root'),
  sshAuthType: z.enum(['password', 'key']).default('password'),
  sshPassword: z.string().optional().default(''),
  sshPrivateKey: z.string().optional().default(''),
  sshKeyPassphrase: z.string().optional().default(''),
  sshHostKeyMode: z.enum(['pin', 'trust', 'skip']).default('trust'),
  sshHostKeySha256: z.string().optional().default(''),
  sshPasswordSet: z.boolean().optional(),
  sshPrivateKeySet: z.boolean().optional(),
}).superRefine((val, ctx) => {
  if (val.sshAuthType === 'password' && val.sshPassword.length === 0 && !val.sshPasswordSet) {
    ctx.addIssue({ code: 'custom', path: ['sshPassword'], message: 'pages.nodes.toasts.fillRequired' });
  }
  if (val.sshAuthType === 'key' && val.sshPrivateKey.length === 0 && !val.sshPrivateKeySet) {
    ctx.addIssue({ code: 'custom', path: ['sshPrivateKey'], message: 'pages.nodes.toasts.fillRequired' });
  }
});

export type ManagedServerRecord = z.infer<typeof ManagedServerRecordSchema>;
export type ManagedServerFormValues = z.infer<typeof ManagedServerFormSchema>;
