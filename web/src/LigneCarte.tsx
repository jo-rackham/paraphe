import { LigneMaire } from "./common.tsx";
import type { MayorCard } from "./types.ts";

/** The shared row, fed with this mode's status and reservation columns. */
export function LigneCarte({
  m,
  onOpen,
}: {
  m: MayorCard;
  onOpen: (insee: string) => void;
}) {
  return (
    <LigneMaire
      m={m}
      status={m.status}
      volunteer={m.volunteer_name}
      onOpen={() => onOpen(m.insee_code)}
    />
  );
}
