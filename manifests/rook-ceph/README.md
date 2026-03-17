# Rook/Ceph Deployment for VM Workloads

HelmChart manifests for K3s/RKE2 deployment.

## Prerequisites

- K3s or RKE2 cluster
- At least one raw block device per node (unformatted)

```bash
kubectl kustomize https://github.com/kubernetes-csi/external-snapshotter/client/config/crd | kubectl apply -f -
kubectl -n kube-system kustomize https://github.com/kubernetes-csi/external-snapshotter/deploy/kubernetes/snapshot-controller | kubectl apply -f -
```

## Deploy

```bash
# 1. Deploy operator and cluster
kubectl apply -f infra/rook-ceph/rook-ceph.yaml

# 2. Wait for cluster to be healthy
watch kubectl -n rook-ceph get cephcluster

# 3. Once healthy, create VolumeSnapshotClass
kubectl apply -f infra/rook-ceph/snapshot-class.yaml
```

## Verify

```bash
# HelmChart status
kubectl -n kube-system get helmchart

# Cluster health
kubectl -n rook-ceph get cephcluster

# Pods
kubectl -n rook-ceph get pods

# Toolbox
kubectl -n rook-ceph exec -it deploy/rook-ceph-tools -- ceph status
```

## Resources Created

| Name | Type | Purpose |
|------|------|---------|
| `rook-ceph-block` | StorageClass | VM disk provisioning |
| `rook-ceph-block` | VolumeSnapshotClass | VM clone snapshots |

## Cleanup

```bash
kubectl delete -f infra/rook-ceph/snapshot-class.yaml
kubectl delete -f infra/rook-ceph/rook-ceph.yaml

# Clean host data (each node)
sudo rm -rf /var/lib/rook
```
