import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge, Tooltip } from "./ui";
import { api } from "../lib/api";

const COLOR: Record<string, string> = { verified: "green", present: "blue", invalid: "red", unsigned: "gray" };

// Cosign signature state of one image (tag or digest) in a registry.
export default function SignatureBadge({ registry, repo, refName }: { registry: string; repo: string; refName: string }) {
  const { t } = useTranslation();
  const [sig, setSig] = useState<any>(null);

  useEffect(() => {
    let live = true;
    setSig(null);
    if (!repo || !refName) return;
    api
      .signature(registry, repo, refName)
      .then((s: any) => live && setSig(s))
      .catch(() => live && setSig(null));
    return () => {
      live = false;
    };
  }, [registry, repo, refName]);

  if (!sig) return null;
  const hint = sig.via_index ? t("signing.viaIndex") : sig.status === "present" && !sig.keys ? t("signing.noKeysHint") : "";
  const badge = (
    <Badge color={COLOR[sig.status] || "gray"} variant={sig.status === "unsigned" ? "outline" : "light"}>
      {t(`signing.${sig.status}`)}
    </Badge>
  );
  return hint ? <Tooltip label={hint}>{badge}</Tooltip> : badge;
}
