import { LigneMaire } from "./common.tsx";
import { equipeQuiTravaille } from "./Team.tsx";
import type { MayorCard } from "./types.ts";

/**
 * The shared row, fed with this mode's status and « who is on it » columns.
 *
 * A card another team is working carries no person — a name does not cross a
 * team — so the row shows the TEAM there. Showing nothing would read as a
 * free card, which is the one thing this column exists to prevent.
 */
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
      volunteer={m.volunteer_name ?? equipeQuiTravaille(m)}
      onOpen={() => onOpen(m.insee_code)}
    />
  );
}
