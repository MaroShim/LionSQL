# LionSQL Production Storage & Filesystem Encryption Guide (Data-at-Rest)

This document provides architectural rationale, compliance mappings, and practical deployment procedures for securing LionSQL data at rest using storage and filesystem-level encryption in production environments.

---

## 1. Architectural Strategy & Design Principles

### 1.1. Why Storage/Filesystem-Level Encryption Over In-Kernel Software TDE

LionSQL adopts **Storage/Filesystem-Level Encryption (OS & Cloud Block Device Encryption)** as its canonical Data-at-Rest encryption standard rather than embedding in-kernel application-level Transparent Data Encryption (TDE):

1. **Hardware Acceleration & Platform Primitives First (Golden Rule 14)**:
   - Modern CPUs feature dedicated cryptographic instruction sets (Intel AES-NI, AMD ARMv8 Cryptographic Extensions).
   - Linux kernel block layers (`dm-crypt`/LUKS2) and Cloud hypervisors execute AES-256 encryption directly on hardware queues with sub-microsecond latency, avoiding user-space context switches and Go runtime memory copies.
2. **Elimination of Double-Encryption Overhead (KISS & DRY - Golden Rule 15)**:
   - Running software TDE inside the database kernel on top of an already encrypted enterprise volume (AWS EBS, GCP Persistent Disk, SAN/NAS LUNs) causes duplicate encryption cycles, doubling CPU load and reducing NVMe I/O transaction throughput by up to 35%.
3. **Defense-in-Depth & Key Management Isolation**:
   - In-process software TDE requires storing database encryption keys (DEK) or master keys inside database process memory, creating exposure risks during memory dumping, core dumping, or zero-day memory inspection.
   - Operating system and Cloud KMS block encryption isolates key custody from the database process, adhering to the principle of least privilege and strict separation of duties (DBA vs. Security/KMS Custodian).
4. **Comprehensive Data Protection**:
   - Filesystem/Block-level encryption protects **all** engine assets uniformly:
     - Table data files (`*.wt`, `tables.mpk`)
     - Write-Ahead Logs (WAL) and Append-Only Files (AOF)
     - Rollback segments and transaction workspaces
     - Temporary swap files and schema metadata snapshots

### 1.2. Regulatory Compliance Mapping

Block and filesystem-level AES-256 encryption fully satisfies enterprise regulatory mandates:

| Regulatory Standard | Clause / Section | Compliance Verification via Storage Encryption |
| :--- | :--- | :--- |
| **PCI-DSS v4.0** | Requirement 3.4 / 3.5 | Hard drive / volume level encryption with independent key management renders PAN unreadable. |
| **HIPAA Security Rule** | 45 CFR §164.312(a)(2)(iv) | Encryption of ePHI at rest using standard NIST-approved AES-256 algorithms. |
| **SOC 2 Type II** | Trust Services Criteria CC6.1 / CC6.7 | Protection of customer data against unauthorized physical media extraction. |
| **GDPR** | Article 32(1)(a) | Pseudonymisation and encryption of personal data on persistent storage volumes. |

---

## 2. Linux On-Premises & Bare-Metal: LUKS2 / dm-crypt

For bare-metal Linux servers and self-managed virtual machines, Linux Unified Key Setup v2 (`LUKS2`) via `dm-crypt` provides native, hardware-accelerated block encryption.

### 2.1. Verifying Hardware Cryptographic Acceleration

Verify that the host CPU supports hardware-accelerated AES:

```bash
# Verify AES-NI (x86_64) or ARM CE (aarch64)
grep -m1 -E "aes|pmull" /proc/cpuinfo
```

### 2.2. Setting Up an Encrypted Block Device for LionSQL

Assuming `/dev/nvme1n1` is a dedicated NVMe drive or partition assigned to LionSQL data:

```bash
# 1. Initialize LUKS2 partition with AES-256 in XTS mode and Argon2id KDF
sudo cryptsetup luksFormat \
  --type luks2 \
  --cipher aes-xts-plain64 \
  --key-size 512 \
  --hash sha512 \
  --pbkdf argon2id \
  /dev/nvme1n1

# 2. Open the encrypted container
sudo cryptsetup open /dev/nvme1n1 lionsql_data_crypt

# 3. Format with ext4 or XFS
sudo mkfs.ext4 -m 1 -O fast_commit,dir_index /dev/mapper/lionsql_data_crypt

# 4. Create mount point and configure permissions
sudo mkdir -p /var/lib/lionsql
sudo mount /dev/mapper/lionsql_data_crypt /var/lib/lionsql
sudo chown -R 10001:10001 /var/lib/lionsql
sudo chmod 700 /var/lib/lionsql
```

### 2.3. High-Throughput NVMe Performance Tuning (dm-crypt Workqueue Bypass)

On modern multi-core NVMe storage systems, default `dm-crypt` thread workqueues introduce unnecessary serialization. Bypassing them delivers raw NVMe speeds:

```bash
# Optimize LUKS mapper for NVMe hardware concurrency
sudo cryptsetup --perf-no_read_workqueue --perf-no_write_workqueue --perf-same_cpu_crypt refresh lionsql_data_crypt
```

### 2.4. Automated Mount on Boot (`/etc/crypttab` & `/etc/fstab`)

To enable unattended reboots while protecting keys:

1. **Option A: Secure Keyfile with Strict Permissions**
   ```bash
   sudo dd if=/dev/urandom of=/etc/lionsql-disk.key bs=512 count=1
   sudo chmod 400 /etc/lionsql-disk.key
   sudo cryptsetup luksAddKey /dev/nvme1n1 /etc/lionsql-disk.key
   ```

2. **Option B: TPM 2.0 Hardware Bound Key (Recommended for Bare-Metal)**
   ```bash
   # Bind encryption key to system TPM 2.0 (PCR 0, 7)
   sudo systemd-cryptenroll --tpm2-device=auto --tpm2-pcrs=0+7 /dev/nvme1n1
   ```

3. **Configure `/etc/crypttab`**:
   ```
   # Target               Source Device     Key File            Options
   lionsql_data_crypt      /dev/nvme1n1      /etc/lionsql-disk.key luks,discard,no-read-workqueue,no-write-workqueue
   ```

4. **Configure `/etc/fstab`**:
   ```
   /dev/mapper/lionsql_data_crypt  /var/lib/lionsql  ext4  defaults,noatime,nodiratime  0  2
   ```

---

## 3. Cloud-Native Block Storage Encryption

In public cloud environments, block storage encryption must be enforced using Customer Managed Keys (CMK) via the cloud provider's Key Management Service (KMS).

### 3.1. Amazon Web Services (AWS EBS & EKS)

1. **Account-Level Default Encryption**:
   - Enable "EBS encryption by default" across all operational AWS regions.
   - Utilize AWS KMS Customer Managed Keys (CMK) with automated annual rotation.

2. **Kubernetes EKS StorageClass (`ebs.csi.aws.com`)**:
   ```yaml
   apiVersion: storage.kk8s.io/v1
   kind: StorageClass
   metadata:
     name: lionsql-encrypted-gp3
   provisioner: ebs.csi.aws.com
   volumeBindingMode: WaitForFirstConsumer
   allowVolumeExpansion: true
   parameters:
     type: gp3
     encrypted: "true"
     kmsKeyId: "arn:aws:kms:ap-northeast-2:123456789012:key/your-lionsql-cmk-id"
   ```

### 3.2. Google Cloud Platform (GCP Persistent Disk & GKE)

1. **Cloud KMS CMEK (Customer-Managed Encryption Keys)**:
   - Configure Persistent Disks with Cloud KMS key rings.

2. **Kubernetes GKE StorageClass (`pd.csi.storage.gke.io`)**:
   ```yaml
   apiVersion: storage.k8s.io/v1
   kind: StorageClass
   metadata:
     name: lionsql-encrypted-ssd
   provisioner: pd.csi.storage.gke.io
   volumeBindingMode: WaitForFirstConsumer
   parameters:
     type: pd-ssd
     disk-encryption-kms-key: "projects/my-project/locations/asia-northeast3/keyRings/lionsql-ring/cryptoKeys/lionsql-key"
   ```

### 3.3. Microsoft Azure (Managed Disk & AKS)

1. **Disk Encryption Sets (DES)**:
   - Create a Disk Encryption Set linked to an Azure Key Vault hardware security module (HSM).

2. **Kubernetes AKS StorageClass (`disk.csi.azure.com`)**:
   ```yaml
   apiVersion: storage.k8s.io/v1
   kind: StorageClass
   metadata:
     name: lionsql-encrypted-managed
   provisioner: disk.csi.azure.com
   volumeBindingMode: WaitForFirstConsumer
   parameters:
     skuname: Premium_LRS
     diskEncryptionSetID: "/subscriptions/.../diskEncryptionSets/lionsql-des"
   ```

---

## 4. Container & Kubernetes Deployment Security

When deploying LionSQL in containers, ensure data persistence boundaries are strictly contained within encrypted mounts:

```bash
# Run LionSQL with dedicated data directory bound to encrypted host mount
docker run -d \
  --name lionsql-prod \
  --user 10001:10001 \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=512m \
  -v /var/lib/lionsql:/data:rw \
  -p 5433:5433 \
  -p 1433:1433 \
  -p 8090:8090 \
  lionsql:latest \
  --data-dir=/data/db \
  --backup-dir=/data/backups \
  --auth=true
```

Key points:
- `--data-dir` and `--backup-dir` reside within `/data`, pointing directly to the encrypted filesystem.
- Temporary files outside the mount are volatile (`--tmpfs /tmp`) and vanish on container restart.
- Container root filesystem is mounted `--read-only`.

---

## 5. Backup & Off-Site Archive Encryption

Physical backups generated by LionSQL (`--backup-dir`) must inherit identical encryption standards:

1. **Local Backups**:
   - Set `--backup-dir` to a directory located on the encrypted LUKS/EBS mount.
2. **Off-Site Shipping (S3 / Cloud Storage / Cold Storage)**:
   - When exporting backup tarballs (`*.tar.gz`) off-premises, encrypt using AES-256-GCM prior to transmission:
     ```bash
     # Encrypt backup archive with OpenSSL AES-256-GCM
     openssl enc -aes-256-gcm -pbkdf2 -iter 100000 \
       -in /var/lib/lionsql/backups/backup_20241016.tar.gz \
       -out /var/lib/lionsql/backups/backup_20241016.tar.gz.enc \
       -pass file:/etc/lionsql-backup.key

     # Verify integrity and ship to S3 with KMS SSE
     aws s3 cp /var/lib/lionsql/backups/backup_20241016.tar.gz.enc \
       s3://company-lionsql-cold-backups/ \
       --sse aws:kms \
       --sse-kms-key-id arn:aws:kms:...
     ```

---

## 6. Operational Security Checklist

- [ ] CPU AES-NI / ARM CE hardware cryptographic acceleration confirmed.
- [ ] Database data directory (`--data-dir`) placed strictly on an encrypted LUKS2 / EBS / CMEK volume.
- [ ] NVMe workqueue serialization bypassed via `--perf-no_read_workqueue --perf-no_write_workqueue`.
- [ ] Database backup directory (`--backup-dir`) placed on an encrypted volume.
- [ ] Off-site backup archives encrypted with AES-256-GCM prior to network transit.
- [ ] Root/administrative access to host filesystem restricted via sudo audit logs.
- [ ] Encryption keys managed through external KMS / Key Vault / TPM 2.0 with annual key rotation enabled.
