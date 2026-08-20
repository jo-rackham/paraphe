// Mass mailing from out/01_maires_cibles_prioritaires.csv.
//
// Produces in out/messages/:
//   emails.csv       one row per reachable mayor: recipient, subject, body
//                    (importable into Thunderbird Mail Merge, Brevo, etc.)
//   courriers.html   the deliverable letters, one per page, ready to print
//                    (address positioned for a window envelope)
//   sans_email.csv   the mayors to handle by postal mail only
//   a_verifier.csv   what is SET ASIDE and why (invalid email, undeliverable
//                    address, missing field) — to handle by hand, never
//                    silently
//
// For per-volunteer personalisation (personal touch, signature in the
// volunteer's name), use the application: this is the uniform "team"
// version.

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { parseRecords, parseRows, writeCsv } from "../noyau/csv.ts";
import {
  createEngine,
  emailAddresses,
  incompleteAddress,
  letterHeader,
  type Mayor,
  MissingField,
  recipientAddress,
} from "../noyau/messages.ts";
import { loadConfig, loadTemplates, ROOT } from "./config.ts";

// The input list and the output directory follow the same contract as the
// API's PARAPHE_CSV: an environment override, a sensible default. It is what
// lets the functional test run the real script against a synthetic list in a
// temporary directory, without touching out/.
const OUT =
  (process.env.PARAPHE_MESSAGES_DIR ?? "").trim() ||
  join(ROOT, "out", "messages");
const TARGETS =
  (process.env.PARAPHE_TARGETS_CSV ?? "").trim() ||
  join(ROOT, "out", "01_maires_cibles_prioritaires.csv");

const PAGE = (
  sender: string,
  address: string,
  placeDate: string,
  body: string,
) =>
  `<div class="lettre">
<div class="expediteur">${sender}</div>
<div class="adresse">${address}</div>
<div class="date">${placeDate}</div>
<div class="corps">${body}</div>
</div>
`;

// A real document, lang included: the letters are proofread on screen by
// volunteers whose screen reader picks its pronunciation engine from the
// page language — without it, French letters are read with the browser's
// default one (WCAG 3.1.1).
const STYLE = `<!doctype html><html lang="fr"><head><meta charset="utf-8">
<title>Courriers aux maires</title>
<style>
body { font: 12pt/1.5 Georgia, serif; margin: 0; }
.lettre { page-break-after: always; padding: 2.5cm 2cm 2cm; min-height: 25cm;
          box-sizing: border-box; }
.expediteur { white-space: pre-line; font-size: 10pt; }
.adresse { white-space: pre-line; margin: 1.2cm 0 0 55%; }
.date { text-align: right; margin: 1cm 0; }
.corps { white-space: pre-line; text-align: justify; }
@media screen { .lettre { border-bottom: 2px dashed #999; } }
</style>
</head><body>
`;

const escapeHtml = (s: string): string =>
  s
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#x27;");

const SET_ASIDE_COLS = [
  "channel",
  "reason",
  "insee_code",
  "commune",
  "department",
  "first_name",
  "last_name",
  "email",
  "phone",
  "postal_address",
  "postal_code",
  "city",
];

const EMAIL_COLS = [
  "email",
  "subject",
  "body",
  "commune",
  "department",
  "last_name",
  "first_name",
  "priority",
  "other_addresses",
];

export function main(): void {
  mkdirSync(OUT, { recursive: true });
  const { campaign: c, phoneOutreach } = loadConfig();
  const engine = createEngine(loadTemplates());
  const text = readFileSync(TARGETS, "utf8");
  const mayors = parseRecords(text) as Mayor[];
  const sourceCols = parseRows(text)[0];
  // THE MAILING WRITES TO FILE 01 AND NOTHING ELSE — ~1,960 endorsers, never
  // the 34,826 of the full base, and never files 02/03, whose people are the
  // ones « vous avez parrainé » would LIE to. With the path overridable, that
  // invariant has to live in the FILE's shape, not in the path. File 01 is
  // the only build output carrying BOTH `small_candidate_endorsements`
  // (which file 04 lacks) and `commune_2026` (which files 02 and 03 lack —
  // build.ts strips it with the rest of notInScope); `rank` is refused on
  // top, being file 04's own signature.
  if (
    !sourceCols.includes("small_candidate_endorsements") ||
    !sourceCols.includes("commune_2026") ||
    sourceCols.includes("rank")
  ) {
    throw new Error(
      `${TARGETS} n'a pas la forme du fichier 01 (parrains uniquement) : ` +
        "le publipostage n'écrit qu'aux maires qui ont déjà parrainé et " +
        "sont toujours en poste. Vérifiez PARAPHE_TARGETS_CSV — la base " +
        "complète, les anciens parrains (02), les non-appariés (03) et " +
        "tout autre fichier sont refusés.",
    );
  }
  // Said out loud BEFORE anything is generated: what is about to be posted,
  // and from which file — the operator's one chance to notice a stale list.
  console.log(`source : ${TARGETS} (${mayors.length} maires)`);
  // THE RETURN ADDRESS IS THE SENDER'S, and the sender is whoever posts the
  // letter. Built from `candidat`, the envelope said the candidate while the
  // body said « je vous écris au nom de » that same candidate and signed with
  // somebody else's name — three identities on one page, up to 1 960 times.
  // The letter stopped being the candidate's when it stopped being written at
  // their « je »; this block had not followed.
  //
  // Joined from the parts that exist: `contact_tel` is one a campaign may
  // leave empty, and an empty line at the top of an envelope window is a
  // line somebody has to explain.
  const sender = [
    [c.signataire, c.signataire_qualite].filter(Boolean).join(", "),
    c.contact_email,
    c.contact_tel,
  ]
    .filter(Boolean)
    .join("\n");

  const withEmail: Record<string, string>[] = [];
  const withoutEmail: Mayor[] = [];
  const pages: string[] = [];
  const setAside: Record<string, string>[] = [];

  const putAside = (m: Mayor, channel: string, reason: string) => {
    setAside.push({
      channel,
      reason,
      insee_code: m.insee_code ?? "",
      commune: m.commune ?? "",
      department: m.department ?? "",
      first_name: m.first_name ?? "",
      last_name: m.last_name ?? "",
      email: m.email ?? "",
      phone: m.phone ?? "",
      postal_address: m.postal_address ?? "",
      postal_code: m.postal_code ?? "",
      city: m.city ?? "",
    });
  };

  for (const m of mayors) {
    let subject: string;
    let body: string;
    let letter: string;
    let address: string;
    try {
      ({ subject, body } = engine.email(m, c, { phoneOutreach }));
      letter = engine.letter(m, c, { phoneOutreach });
      address = recipientAddress(m);
    } catch (e) {
      // Data unusable for THIS mayor: set them aside, never generate an
      // approximate message. An InvalidTemplate affects all 1,972 rows: it
      // propagates, rather than replacing the previous mailing with four
      // empty files while exiting 0.
      if (e instanceof MissingField) {
        putAside(m, "email+courrier", e.message);
        continue;
      }
      throw e;
    }

    const { valid, rejected } = emailAddresses(m);
    // A mayor without an address AND without a deliverable letter is not
    // "to handle by mail": they are reachable through no channel, and the
    // two files contradicted each other.
    const badAddress = incompleteAddress(m);
    if (valid.length) {
      // one row per mayor: sending twice to the same small town hall looks
      // like mass mailing, which is precisely what we avoid
      withEmail.push({
        email: valid[0],
        subject,
        body,
        commune: m.commune ?? "",
        department: m.department ?? "",
        last_name: m.last_name ?? "",
        first_name: m.first_name ?? "",
        priority: m.priority ?? "",
        other_addresses: valid.slice(1).join(" "),
      });
    } else if (!badAddress) {
      withoutEmail.push(m);
    } else {
      putAside(
        m,
        "aucun",
        "ni adresse email exploitable, ni courrier " +
          `distribuable (${badAddress})`,
      );
    }
    if (rejected.length) {
      // listed as they are: this file is read by volunteers, not by a
      // program
      putAside(
        m,
        "email",
        `adresse(s) non conforme(s) : ${rejected.join(", ")}`,
      );
    }

    if (badAddress) {
      if (valid.length) putAside(m, "courrier", badAddress);
    } else {
      pages.push(
        PAGE(
          escapeHtml(sender),
          escapeHtml(address),
          escapeHtml(letterHeader(m, c)),
          escapeHtml(letter),
        ),
      );
    }
  }

  writeFileSync(
    join(OUT, "emails.csv"),
    writeCsv(EMAIL_COLS, withEmail),
    "utf8",
  );
  writeFileSync(
    join(OUT, "courriers.html"),
    `${STYLE + pages.join("\n")}</body></html>\n`,
    "utf8",
  );
  writeFileSync(
    join(OUT, "sans_email.csv"),
    writeCsv(sourceCols, withoutEmail as Record<string, unknown>[]),
    "utf8",
  );
  writeFileSync(
    join(OUT, "a_verifier.csv"),
    writeCsv(SET_ASIDE_COLS, setAside),
    "utf8",
  );

  console.log(
    `emails.csv: ${withEmail.length} | courriers.html: ${pages.length} ` +
      `letters | sans_email.csv: ${withoutEmail.length} | ` +
      `a_verifier.csv: ${setAside.length} set aside`,
  );
  for (const e of setAside) {
    console.log(
      `  set aside [${e.channel}] ${e.commune} (${e.insee_code}): ${e.reason}`,
    );
  }
  // An empty output is a failure, not a result: without this exit code,
  // `task messages` succeeded while having replaced the previous mailing
  // with four empty files.
  if (!withEmail.length && !pages.length) {
    console.error(
      "no message produced: the four files of out/messages/ have " +
        "been replaced with empty ones. Check the templates and the " +
        "configuration before rerunning.",
    );
    process.exitCode = 1;
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main();
