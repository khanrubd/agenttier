# Sandbox cloning

`POST /api/v1/sandboxes/{id}/clone` creates a new sandbox whose workspace is hydrated from a CSI VolumeSnapshot of the source sandbox's PVC. The new sandbox inherits the source's spec — template, image, env, ports, agent harness — so the result is a faithful "fork" of the original at the moment the snapshot is taken.

## Quick start

```bash
curl -X POST https://your-agenttier/api/v1/sandboxes/my-sandbox/clone \
  -H 'Authorization: Bearer ...' \
  -H 'Content-Type: application/json' \
  -d '{"name": "my-sandbox-fork"}'
```

Response (202 Accepted):

```json
{
  "name": "my-sandbox-fork",
  "namespace": "default",
  "snapshot": "my-sandbox-snap-1780023662",
  "clonedFrom": "my-sandbox",
  "phase": "Pending",
  "message": "Clone in progress. Poll GET /api/v1/sandboxes/my-sandbox-fork for status..."
}
```

The new sandbox enters `Creating` while the CSI driver hydrates the volume from the snapshot. On EBS this typically takes 30–90 seconds for a 20 GiB PVC. Poll `GET /api/v1/sandboxes/my-sandbox-fork` until `phase: Running`.

## Request body

| Field | Type | Default | Notes |
|---|---|---|---|
| `name` | string | `<source>-clone-<unix-ts>` | Must satisfy RFC 1123 label rules. |
| `snapshotClass` | string | cluster default `VolumeSnapshotClass` | Override when you need a non-default driver or retention policy. |

Both fields are optional. Body itself is optional — `POST .../clone` with no body produces a clone using all defaults.

## What gets cloned

| Resource | Cloned |
|---|---|
| Workspace contents (the PVC) | yes (byte-identical via VolumeSnapshot) |
| Sandbox spec (template, env, ports, agent harness) | yes (deep-copied from source) |
| Status fields (phase, podName, conditions) | no (regenerated for the new sandbox) |
| `CreatedBy` | re-stamped from the cloning user, not the original creator |

The new sandbox also stamps `status.clonedFrom: <source>` for auditability and `spec.cloneFromSnapshot: <snapshot-name>` so a `kubectl get -o yaml` on the cloned CR makes the lineage obvious.

## Cluster prerequisites

CSI VolumeSnapshot infrastructure must be installed on the cluster. On EKS this is **not** part of the default EBS CSI install — you must add it explicitly:

```bash
# 1. Snapshot CRDs
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v6.3.3/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v6.3.3/client/config/crd/snapshot.storage.k8s.io_volumesnapshotclasses.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v6.3.3/client/config/crd/snapshot.storage.k8s.io_volumesnapshotcontents.yaml

# 2. Snapshot controller
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v6.3.3/deploy/kubernetes/snapshot-controller/rbac-snapshot-controller.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v6.3.3/deploy/kubernetes/snapshot-controller/setup-snapshot-controller.yaml

# 3. Default VolumeSnapshotClass for EBS
cat <<EOF | kubectl apply -f -
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: ebs-snapshot
  annotations:
    snapshot.storage.kubernetes.io/is-default-class: "true"
driver: ebs.csi.aws.com
deletionPolicy: Delete
EOF
```

Other clouds: replace `ebs.csi.aws.com` with `pd.csi.storage.gke.io` (GKE), `disk.csi.azure.com` (AKS), or whatever CSI driver name your cluster uses.

The IAM role attached to your CSI controller needs `ec2:CreateSnapshot`, `ec2:DeleteSnapshot`, `ec2:DescribeSnapshots`, and `ec2:CreateTags` on EBS. The default `Amazon_EBS_CSI_Driver` AWS-managed policy includes them; if you've cut a custom policy, double-check.

If the snapshot infrastructure is missing, `POST /clone` returns `500 failed to create VolumeSnapshot: no matches for kind "VolumeSnapshot" in version "snapshot.storage.k8s.io/v1"`.

## Lifecycle

The VolumeSnapshot the Router creates is **not owner-referenced to the source sandbox** — it's a standalone object. That means deleting the source sandbox does not cascade-delete the snapshot. The snapshot lives until you explicitly delete it (or until its `VolumeSnapshotClass` retention policy fires, if you configure one).

The cloned sandbox's PVC, on the other hand, is independent of the snapshot — once the CSI driver finishes hydrating the new volume, you can delete the snapshot without affecting the clone.

To delete the snapshot manually:

```bash
kubectl delete volumesnapshot -n <namespace> <source>-snap-<ts>
```

## Limits

- **Same-namespace only.** The snapshot and the cloned Sandbox CR live in the source's namespace. Cross-namespace clones are rejected.
- **Source must have a PVC.** Sandboxes that haven't reached at least the `Running` (or `Stopped`) phase don't have a PVC yet and can't be cloned. The endpoint returns 400 with a clear error.
- **Same template required.** The cloned sandbox uses the source's template. Cloning across templates would produce a sandbox whose pod expects different image/env contracts than the workspace bytes hydrated into; the spec keeps things consistent on purpose.
- **Soft cap on storage class compatibility.** The cloned PVC inherits the source's `storageClassName`. If your cluster has multiple CSI drivers, ensure the snapshot class you pass matches the source PVC's driver — cross-driver hydration is not supported.

## Programmatic access

Python SDK (sync and async):

```python
from agenttier import AgentTierClient

client = AgentTierClient(...)

# Take a clone of an existing sandbox
clone = client.sandboxes.clone(
    "my-sandbox",
    name="my-sandbox-fork",
)
print(clone.snapshot)  # the VolumeSnapshot name

# Wait for the clone to be Running
client.sandboxes.wait_until_running(clone.name, timeout=180)
```

CLI:

```bash
agenttier sandbox clone my-sandbox --name my-sandbox-fork
```
