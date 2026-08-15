# paraphe — maires parrains de petits candidats (présidentielle)

Objectif : identifier les maires qui ont parrainé des candidats peu connus /
marginaux malgré la publicité des parrainages (depuis 2017), et qui sont
**toujours en poste** (post-municipales mars 2026), avec les contacts de leur
mairie — pour les solliciter en priorité pour les présentations 2027.

`devbox install && task` — `task all` refait tout (download + build) ;
`task messages` = publipostage de masse (out/messages/) ; `task api` = API
d'équipe sur http://127.0.0.1:8047, qui sert aussi l'interface construite
(`task web-build`) ; `task web` = interface en développement sur :5180, avec
/api proxyfié vers l'API.

## Outils de campagne
- `config/campagne.yaml` : candidat, signataire, contacts — À REMPLIR.
- `modeles/*.txt` : textes à trous (email/courrier/téléphone), éditables sans
  toucher au code ; les {placeholders} sont vérifiés (erreur explicite si
  inconnu). `modeles/*.md` = notes de stratégie par canal.
- `noyau/` (TypeScript, sans dépendance) : moteur de messages **unique**
  (salutation genrée, date fr, touche personnelle du bénévole), lecture/
  écriture CSV, normalisation des noms et port fidèle de
  `difflib.SequenceMatcher`. Partagé par `web/` et `outils/`.
- `outils/` (TypeScript lancé par Node, sans compilation) : `build.ts`
  (croisement des sources), `messages-masse.ts` (publipostage), `faux-jeu.ts`
  (jeu synthétique pour la CI), `telecharger.sh` (bash).
- `api/` : **API JSON en Go** (pgx, `PARAPHE_DATABASE_URL` ; `task db` démarre
  une base en local). Elle ne rend aucun HTML et ne génère aucun message :
  elle sert l'interface construite (`web/dist`) et des données. Une seule
  image, sans état, plusieurs instances devant la même base.
- `web/` : **interface React + TypeScript, trois modes**. `App.tsx` interroge
  `/api/config` au chargement : une API répond avec une campagne → mode
  équipe ; elle répond « instance » (apex d'une instance multi-campagnes) →
  accueil public et modération (`Instance.tsx`) ; rien ne répond (GitHub
  Pages) → mode navigateur, tout en IndexedDB. Le choix se fait à
  l'exécution, pas à la compilation — une version construite avec le mauvais
  drapeau ne se remarquerait qu'en production. `common.tsx` porte la fiche
  (donc la génération des messages) et le guide, partagés par les deux modes.
  Mode équipe, pour bénévoles non-techniciens : lots de 10 réservés (les
  mieux notés d'abord), fiche par maire (email retouchable + mailto, lettre
  imprimable, script d'appel avec horaires), statuts + notes partagés, suivi
  30 départements / plafond 50, export CSV, défilement infini.
  Le travail d'équipe vit en base PostgreSQL — À SAUVEGARDER (`task backup`).
- **Comptes et équipes locales** : accès par compte individuel (email + mot
  de passe hashé), trois rôles — coordination (voit tout, crée les équipes
  et les référents), référent (ouvre les accès bénévoles de SON équipe),
  bénévole. Une équipe = un nom + des départements ; elle ne pioche que dans
  son périmètre, ne voit que ses réservations, et une fiche réservée
  ailleurs lui est refusée (403). Les compteurs de campagne restent visibles
  par tous, sans noms. Amorçage par `PARAPHE_ADMIN_EMAIL` /
  `_PASSWORD` : sans compte de coordination, l'app refuse d'ouvrir plutôt
  que de laisser entrer.
- Hébergement : petit VPS + reverse-proxy HTTPS (caddy) ou tunnel
  (cloudflared/tailscale) devant le port 8047.
- `GUIDE.md` : méthodo complète pour l'équipe (importé tel quel par
  l'interface — `?raw` + marked — donc rendu dans les DEUX modes, et page
  d'atterrissage après connexion). Inclut les règles d'envoi qui
  évitent le classement en spam (adresse perso par bénévole, 20-25/jour) et
  la cagnotte courrier via le mandataire financier.
- `DEPLOIEMENT.md` + `Dockerfile` + `docker-compose.yml` : toute la config
  de campagne est surchargeable au runtime par `PARAPHE_*` (candidat,
  descriptions, contacts, taille de lot, comptes, secret, HTTPS).
  Config au gabarit = refus de générer le publipostage, bandeau
  d'avertissement dans l'app.
- Tag `parrainage_theme_democratique` (142 cibles) : a parrainé une
  candidature portant sur le fonctionnement démocratique
  (Marchandise/LaPrimaire, Egger/RIC, Koenig/subsidiarité, Jardin, Nikonoff,
  Faudot, Troadec). Le tag qualifie **un acte public constaté**, jamais une
  conviction — ne pas le renommer en « sensibilité » : on ne présume pas de
  la sincérité d'un élu. Filtrable dans la liste et à la prise de lot.
- **Base complète `out/04_base_complete.csv` (34 826 maires), 3 rangs** :
  `has_endorsed` (~1 960, filtre par défaut dans l'app) / `commune_has_endorsed` (3 049 :
  la commune a un précédent, le maire a changé) / `no_signal` (29 805).
  Nécessaire : ~1 960 cibles à 10-15 % de conversion ne produisent pas 500
  signatures. Un rang « a parrainé à gauche et à droite » a été essayé puis
  retiré : 11 maires seulement, pour une table de familles politiques
  éditorialement contestable — le coût dépassait le signal.
  **Le rang commande le modèle de message** (`modeles/*_decouverte.txt`) :
  « vous avez présenté X » n'est dit qu'au rang `has_endorsed`. `task messages` ne
  publiposte QUE le fichier 01 — 34 800 emails de masse seraient du spam.

## Sources (toutes ouvertes ; domaine public pour les parrainages du
## Conseil constitutionnel, Licence Ouverte/Etalab pour le RNE)
- Parrainages validés 2017 et 2022 : Conseil constitutionnel via data.gouv.fr
  (publication finale nominative : élu, mandat, commune, candidat).
- Répertoire national des élus (RNE), fichier maires : ministère de
  l'Intérieur, data.gouv.fr, mis à jour en continu (état post-municipales 2026).
- Annuaire de l'administration (DILA, api-lannuaire.service-public.fr) :
  email / adresse / téléphone / site de chaque mairie, clé = code INSEE.
  C'est LA base de contacts — pas besoin de scraper les sites municipaux.

## Décisions verrouillées
- **Tout le code est en anglais** (identifiants, commentaires, SQL, clés
  JSON, routes, en-têtes CSV, valeurs de statut/rang/rôle) — réécriture
  prouvée à l'octet près contre l'ancienne.
  Les variables d'environnement `PARAPHE_*` en font partie : elles
  s'adressent à qui exploite le service, pas à un bénévole
  (`PARAPHE_PG_PASSWORD`, `PARAPHE_BASE_DOMAIN`, `PARAPHE_BATCH_SIZE`…).
  `PARAPHE_BASE_PATH` est le chemin de base Vite, à ne pas confondre avec
  `PARAPHE_BASE_DOMAIN` — d'où le suffixe.
  **Restent en français** : les chaînes affichées aux utilisateurs, les
  {placeholders} des modèles, les clés de `campagne.yaml`, les commentaires
  de `chart/values.yaml` et `.env.exemple` (lus par l'opérateur de
  campagne), les noms des fichiers générés
  (`01_maires_cibles_prioritaires.csv`…), `rapport.md`, GUIDE.md et
  DEPLOIEMENT.md.
- Classification des candidats dans `outils/build.ts` (TIER_A/TIER_B),
  transparente et ajustable ; le rapport liste chaque candidat avec son total
  et sa classe. A = vrais candidats (≥5 parrainages) non qualifiés, hors
  personnalités mainstream ; B = outsiders qualifiés de justesse
  (Arthaud, Poutou, Lassalle, Cheminade, Asselineau 2017) + Taubira 2022.
  Les parrainages « d'appel » à des non-candidats (Pesquet, Hollande 2022…)
  et les personnalités d'appareil (Juppé, Yade…) ne comptent pas.
- Score : A=2, B=1, +1 si parrainage petit candidat en 2017 ET 2022.
  P1 = au moins un A. Un signataire B des deux années (score 3) passe devant
  un A isolé (score 2) : la récidive prédit mieux que l'intensité.
- Cible = personne (le maire), pas la commune : si le maire a changé, la
  commune sort de la liste (fichier 02 pour mémoire).
- Sorties en CSV `;` UTF-8-BOM (Excel/LibreOffice-friendly). Le fichier 01
  contient les 3 canaux (email, téléphone + horaires_mairie, adresse postale)
  et deux colonnes de publipostage (candidat_recent en prose, annee_recente).
- `modeles/` : trames email / courrier / téléphone pour la sollicitation,
  séquencement décrit dedans (email à l'échelle → courrier P1/récidivistes
  tôt → téléphone en relance ciblée pendant la fenêtre).

## Pièges payés
- **L'identité d'un parrain = commune + nom + PRÉNOM, partout.** Trois
  endroits distincts l'ont oublié, chacun produisant de faux « merci pour
  votre parrainage » : (1) la clé d'agrégation des parrainages — sans
  prénom, Mélina et Gilles Gardien fusionnent et le maire actuel hérite du
  parrainage de l'autre (5 faux positifs purs) ; (2) le rapprochement au
  RNE — le nom seul sur-matche les successions (63 cas) ; (3) la dédup par
  INSEE. Un INSEE dupliqué en sortie = assert.
- **Le code sexe du RNE (colonne 8) est le discriminant décisif** : il
  tranche Christian→Christine (ratio 0.89, invisible à l'orthographe) et
  autorise en retour une tolérance basse sur les coquilles (Henry/Henri).
  Il fournit aussi la civilité de sortie : celle du Conseil constitutionnel
  contredit le RNE sur 2 lignes.
- **Comparer les prénoms ENTIERS** : réduits au premier jeton, Marie-Cécile
  et Marie-Ève deviennent la même personne. Troncature admise
  (Jean-Louis ⊇ Louis), contradiction refusée.
- **Outre-mer** : Martinique, Guyane, Polynésie, Nouvelle-Calédonie et SPM
  ont un libellé de département VIDE au RNE — leur nom est dans la colonne
  « collectivité à statut particulier ». Sans ce repli, 139 communes sont
  inatteignables (12 cibles perdues, dont 7 P1 parrains d'Oscar Temaru).
- **Ne jamais affirmer ce qui n'est pas établi** : un rapprochement de
  commune approximatif (fuzzy à 0.87 confond Goncourt/Voncourt) suivi d'un
  nom différent ne prouve pas une succession. Cutoff à 0.93, et le doute va
  en 03 « à vérifier », jamais en 02 « successeur en place ».
- État civil + département de sortie = ceux du RNE (les fichiers
  parrainages varient : noms composés, « Corse du Sud »/« Corse-du-Sud »).
- Le CSV parrainages 2022 a des doubles espaces dans certains noms de
  candidats (« SMATI  Rafik ») → tout passe par collapse().
- RNE : noms parfois composés (nom de naissance + usage) → match « un des
  tokens » ; communes fusionnées/renommées depuis 2017 → fallback fuzzy
  (difflib ≥0.87) puis recherche de la personne dans tout le département.
- Annuaire : `pivot like "mairie"` attrape aussi annexes/déléguées → filtre
  type_service_local ∈ {mairie, mairie_com} + dédup par INSEE (nom le plus
  court = fiche principale).
- Les champs annuaire (telephone, adresse, site) sont du JSON embarqué dans
  le CSV.

- **Réécriture Python -> TypeScript, prouvée par l'octet** : le croisement,
  le publipostage et le jeu synthétique ont été portés, puis validés en
  comparant les sorties aux fichiers produits par la version Python — les
  quatre CSV, `emails.csv`, `courriers.html` et `sans_email.csv` sont
  identiques octet pour octet sur les 34 826 lignes réelles. Le seul écart
  est voulu : `a_verifier.csv` n'affiche plus un `repr` Python (`['x']`) mais
  la liste des adresses en clair. **Refaire cette comparaison** avant de
  toucher au croisement.
- **`difflib.SequenceMatcher` n'a pas d'équivalent hors Python.** Le seuil de
  0,93 sur les noms de communes a été réglé contre SON `ratio()` : une autre
  distance change les rapprochements, donc les maires ciblés, sans rien
  signaler. Le port est dans `noyau/texte.ts`, vérifié à zéro écart sur
  4 000 paires réelles et 3 120 recherches de plus proche voisin, et figé par
  `noyau/texte.test.ts`. L'heuristique « autojunk » (b ≥ 200 éléments) n'est
  pas reproduite : le port lève plutôt que de diverger en silence.
- **Le CSV de Python finit ses lignes en CRLF** (`csv.writer`, dialecte
  excel). `noyau/csv.ts` fait pareil — sans quoi la comparaison octet pour
  octet ci-dessus était impossible.
- **`current_setting('x', true)` ne rend NULL qu'avant le premier réglage.**
  Un réglage posé à portée TRANSACTION redevient la **chaîne vide** — pas
  « absent » — à la fin de la transaction. Sur une connexion de pool déjà
  servie, la politique RLS évaluait donc `''::int` et levait « invalid input
  syntax for type integer », loin de la cause. D'où le `NULLIF(…, '')`.
- **`FOR UPDATE SKIP LOCKED` ne protège pas une ligne qui n'existe pas.**
  Le travail vivant désormais dans `assignments`, une fiche libre n'a AUCUNE
  ligne à verrouiller : l'attribution est l'INSERT lui-même, avec
  `ON CONFLICT (org_id, insee_code) DO UPDATE … WHERE volunteer IS NULL`.
  PostgreSQL n'en laisse aboutir qu'un, le perdant n'est pas compté.
- **Un tour d'attribution vide ne signifie pas « vivier épuisé ».** Tous les
  bénévoles visent les mieux notés : le perdant d'une course voit son
  instantané entièrement pris et repartirait avec « le vivier est épuisé »
  devant un vivier plein. D'où la boucle bornée (8 tours) et la question posée
  explicitement — reste-t-il des fiches ? — avant d'afficher ce message.
- **Une sonde Kubernetes s'adresse à l'IP du pod**, donc avec un `Host` qui ne
  correspond à aucun sous-domaine. `/api/config` lui répond 404 en
  multi-campagnes et AUCUN pod ne devient prêt : d'où `/health/db`, qui
  interroge la base sans résoudre de campagne.
- **Un « : » non protégé dans un `run:` casse le fichier YAML ENTIER.**
  Le Taskfile l'a payé, `.github/workflows/ci.yml` le portait encore :
  GitHub aurait rejeté le workflow complet, invisible tant que rien n'était
  poussé. Un test (`outils/deploiement.test.ts`) balaie désormais les
  workflows et le Taskfile.
- **`web/src/modeles/` était une copie de `modeles/`** : deux jeux de textes
  identiques, donc une divergence en attente. Une seule source désormais,
  importée depuis la racine par l'interface comme par le publipostage.

## Vérifications faites (12/08/2026)
- Totaux parrainages = chiffres officiels (13 427 en 2022, 14 296 en 2017).
- Rétention plausible : 52 % des signataires 2022 encore maires, 22 % des
  signataires 2017-seul (deux municipales plus tard).
- Échantillon de 8 « maire différent » : tous de vraies successions.
- 3 emails recontrôlés en live contre l'API annuaire : identiques.
- **Revue adversariale (3 agents, opus, effort max)** sur les données, la
  génération de messages et l'app+déploiement. Données : 3 findings
  critiques + 2 hauts, tous reproduits puis corrigés (cf. pièges payés) ;
  1 951 → 1 972 cibles (−6 faux positifs, +12 outre-mer, +4 variantes de
  prénom récupérées). Messages : gabarit non rempli qui partait en clair
  (1 934 emails), 15 adresses email invalides, 1 lettre non distribuable —
  corrigés, écarts désormais listés dans out/messages/a_verifier.csv. App :
  aucun finding critique ni haut ; bypass d'auth, double attribution de
  lot, XSS stocké et injection SQL attaqués et réfutés par exécution ;
  4 durcissements bas appliqués (offset borné, SameSite, cookie Secure via
  PARAPHE_HTTPS, PARAPHE_SECRET_KEY indépendant de tout secret saisi par un humain).

## Revue adversariale, tour 3 (13/08/2026) — six agents, six surfaces
- **Un faux positif nominatif partait vraiment** : une commune absente du
  RNE n'est PAS une commune fusionnée (912 communes ont une mairie et aucune
  ligne RNE). Le repli « retrouvé par nom » attribuait alors le parrainage à
  un homonyme du département — Régine THOMAS, maire de Robécourt,
  remerciée d'une signature donnée à 130 km, au Puid, commune jamais
  fusionnée qui possède toujours l'INSEE 88362. **L'INSEE de la commune
  signée tranche** : identique = renommage (Vertus et Blancs-Coteaux sont
  tous deux 51612), différent = deux communes. Les mairies **déléguées**
  sont exclues de l'index : elles gardent le code historique, et les
  compter refuserait les fusions que le repli existe pour traiter.
- **Choisir le modèle par le rang ne suffit pas** : `fields()` fournissait
  `candidat_recent`/`annee_recente` à tous les rangs, donc coller la phrase
  de remerciement dans un modèle `_decouverte` imprimait « En , vous avez
  présenté . » à 32 866 maires, en silence. Les placeholders de parrain
  n'existent plus que pour un parrain, ceux de découverte que pour les
  autres — le collage lève désormais en nommant le modèle.
- **Le garde-fou de gabarit existait en double, et le plus faible commandait** :
  Go était aveugle à `{candidat}`, à un accent décomposé et à une espace de
  largeur nulle, et c'est Go qui arme le bandeau. `noyau/gabarit-cases.json`
  est lu par les deux langages ; ajouter un cas oblige les deux à répondre.
- **Les sources sont lues PAR POSITION** (r[4] = commune, r[8] = code sexe) :
  une colonne insérée en amont décalait toutes les identités et produisait
  34 826 lignes de charabia, exit 0, CI verte, publication sur Pages. En-têtes
  vérifiées à l'index lu + plancher de rendement. Au passage : les deux
  fichiers de parrainages n'écrivent PAS leur dernière colonne pareil
  (« Candidat » en 2022, « Candidat-e parrainé-e » en 2017).
- **IndexedDB ne remodèle une base que si la VERSION monte.** Les magasins
  renommés à version constante laissaient toute base déjà ouverte sur
  l'ancien schéma : chaque lecture levait, l'export de secours compris.
- **Quatre tests certifiaient sans rien prouver** (trouvés par mutation) :
  le canari YAML ignorait la forme `cmds:` du Taskfile — celle où le bug
  avait été payé ; « l'écriture chez le voisin est refusée » était vacante
  sur 3 tables sur 4 (les contraintes NOT NULL refusaient à la place de la
  politique) ; le chemin d'import n'avait aucun test alors qu'il est le seul
  à SUPPRIMER dans `mayors` ; l'ordre « les mieux notés d'abord » n'était
  affirmé nulle part.
- **Deux parcours e2e écrits le matin même verrouillaient un défaut** : la
  sauvegarde n'affirmait que le suivi (la campagne ne revenait pas), l'autre
  affirmait « volunteer » à l'écran. Un test qui passe pour une mauvaise
  raison est pire que pas de test.
- Reste : rôles affichés en français, signataire câblé (les emails d'équipe
  partaient signés d'un autre), en-tête du CSV vérifiée en mode navigateur,
  démarrage refusé sans compte de coordination.

## Boucle adversariale, tours 4 à 7 (13/08/2026) — la leçon
**Chaque correctif important a demandé un tour de plus pour être juste.**
C'est le résultat principal de la journée, et la raison de ne pas conclure
sur un tour vert :
- garde-fou de gabarit : Go aveugle à l'espace insécable → corrigé → JS
  aveugle à U+0085, dans l'autre sens (`\s` diffère entre les deux
  langages). `noyau/gabarit-cases.json` est lu par Go ET par TS ; ajouter
  un cas oblige les deux à répondre.
- sauvegarde navigateur : garde « magasin vide » qui ne se déclenchait
  jamais (la première visite écrit la clé `liste`) → fusion clé par clé.
- écran bloqué sur « Chargement… » : promesse qui ne se résout jamais →
  promesse qui rejette → **écran identique**, l'appelant n'attrapait pas.
- config de campagne écrasée au redémarrage : corrigée dans le code (les
  `PARAPHE_*` explicites seuls surchargent), puis **réarmée par le chart**,
  qui émettait les neuf clés du gabarit inconditionnellement. Les valeurs
  de `chart/values.yaml` et de `.env.exemple` sont vides à dessein.
- élision : ajoutée → fausse sur le h (la toponymie communale française est
  germanique/normande, donc h aspiré : « de Havange », pas « d'Havange »).
- pagination : OFFSET sautait un maire → keyset → curseur illisible qui
  rejouait la page 1 et bouclait à l'infini.
- **Six tests écrits le même jour certifiaient sans rien prouver** — dont
  une garde CI sur `process.env.CI` qui rendait le job `outils` rouge à
  chaque push, et `release.yml` passe par la CI : plus rien n'aurait pu
  être publié.
- Le défilement infini rejouait une page en échec 60 fois/s : 599 requêtes
  en 10 s depuis un seul onglet, un déni de service auto-infligé.
Méthode qui a tout trouvé : **muter le code et exiger que le test rougisse**.

## Multi-campagnes (une instance, plusieurs organisations)
- `PARAPHE_BASE_DOMAIN` vide = **mono-campagne**, comportement d'origine :
  tout hôte sert la campagne d'amorçage. Renseigné = chaque campagne sur
  `<slug>.<domaine>`, l'apex servant l'accueil public et la modération.
  Le mode ne se choisit pas à la compilation : il se lit dans l'en-tête
  `Host` de chaque requête.
- **La liste des maires reste COMMUNE et en lecture seule** (donnée publique,
  identique pour tous). Seules les colonnes de travail sont sorties de
  `mayors` vers `assignments(org_id, insee_code, team_id, volunteer,
  status, updated_at)`. Dupliquer 34 826 lignes par campagne serait le
  mauvais choix.
- **Le cloisonnement est dit DEUX FOIS, et chaque mur tient seul.**
  1. PostgreSQL : `ENABLE` + `FORCE ROW LEVEL SECURITY` sur `assignments`,
     `notes`, `teams`, `accounts`, politique sur `app.org_id`, posé par
     `set_config(…, true)` — portée TRANSACTION — au début de chaque requête.
     `TestRLSHoldsWithoutApplicationFilter` interroge les tables sans le
     moindre filtre et vérifie qu'il ne sort rien.
  2. L'application : **toute requête touchant une table cloisonnée nomme la
     campagne** (`scopeOrg(r)`). `TestWallsHoldWithoutRLS` fait tourner
     l'application sous un rôle **privilégié** — RLS neutralisé, avec un
     témoin qui le prouve — et vérifie qu'aucune campagne ne voit celle d'à
     côté.
  Raison du doublon : RLS ne s'applique qu'à un rôle ni `SUPERUSER` ni
  `BYPASSRLS`, et ce n'est **pas** le défaut (l'image PostgreSQL officielle
  fait de `POSTGRES_USER` un superutilisateur). Un rôle privilégié dégrade
  désormais de « aucun mur » à « un mur ».
  Ce n'est pas une propriété qu'on tient à la discipline sur 30 sites :
  `TestEveryQueryOnAWalledTableNamesTheCampaign` lit le paquet en AST et
  exige, **par alias de table**, le prédicat `org_id` — un seul croisement
  est exempté, `db.go:removeStale`, parce que la liste des maires est
  commune et qu'y supprimer une ligne doit tenir compte de TOUTES les
  campagnes.
  Dans `assignmentJoin`, le prédicat est dans la **condition de jointure**,
  jamais dans le `WHERE` : en `WHERE`, la jointure externe devient interne et
  tous les maires que personne n'a encore pris disparaissent.
  La revue avait déjà trouvé deux trous de périmètre dans ce code
  (`/export.csv`, `/statut`) ; un troisième entre campagnes ferait fuiter le
  travail d'une campagne chez une autre.
- **Deux périmètres sentinelles**, impossibles en base (les identités
  commencent à 1) : `0` = instance (apex, ne voit AUCUNE ligne de travail),
  `-1` = maintenance (import et migrations, traversent les campagnes).
  Aucune requête HTTP ne peut atteindre `-1` : la résolution ne rend que des
  identifiants lus en base ou `0`.
- Rôle `administration` : valide les demandes d'hébergement, vit dans le
  périmètre d'instance, ne lit aucune donnée de campagne. Il ne s'obtient que
  par amorçage — `validRole` ne connaît que les trois rôles de campagne, ce
  qui empêche une coordination de se le fabriquer.
- La configuration de campagne vit **en base, par organisation**, éditable par
  la coordination (onglet « Mon équipe »). Les `PARAPHE_*` de campagne
  n'amorcent plus que la première.
- Formulaire public de demande + modération : une demande ne crée rien.
  Sans ça, le premier abus est le squat du nom d'un candidat, sans recours
  pour la campagne squattée.

## Sécurité et exploitation
- Modèle d'accès = **comptes individuels** (email + mot de passe hashé) avec
  cloisonnement par équipe locale, et par campagne quand l'instance en héberge
  plusieurs. `PARAPHE_ADMIN_EMAIL`/`_PASSWORD` amorcent la
  coordination, `PARAPHE_INSTANCE_ADMIN_*` l'administration d'instance.
  `PARAPHE_HTTPS=1` derrière un proxy TLS (cookie Secure).
  `PARAPHE_SECRET_KEY` : obligatoire en multi-instance ; sinon un secret
  aléatoire est tiré au premier démarrage et conservé en base. Les valeurs
  d'exemple du dépôt sont refusées au démarrage (elles sont publiques).
- **Les valeurs réelles ne vont JAMAIS dans un fichier versionné** :
  `.env` (compose) et `config/campagne.local.yaml` (surcharge locale) sont
  ignorés ; `docker-compose.yml` et `config/campagne.yaml` sont des
  gabarits publics.
- L'import au démarrage est un UPSERT : les données (email, score, tags)
  sont rafraîchies, les colonnes de travail (volunteer, status, updated_at) jamais
  touchées ; une cible retirée du CSV est supprimée si intacte, signalée
  « RETIRÉ » si déjà travaillée. Schéma migré par ALTER TABLE.
- **Le rôle PostgreSQL de l'application ne doit être ni superutilisateur ni
  BYPASSRLS** : un rôle privilégié traverse RLS, `FORCE` compris, et
  l'application tournerait comme si le cloisonnement n'existait pas. C'est le
  défaut par défaut (l'image officielle fait de `POSTGRES_USER` un
  superutilisateur), donc le démarrage le REFUSE en multi-campagnes et
  l'avertit en mono. Les tests d'intégration créent exprès un rôle sans
  privilège : joués sous le compte d'administration, ils vérifieraient un
  cloisonnement qui n'existe pas en production.
- **Limite ASSUMÉE : le rôle applicatif est PROPRIÉTAIRE des tables.** Un seul
  rôle est provisionné, celui que CNPG génère, et l'application fait son propre
  DDL au démarrage. `FORCE ROW LEVEL SECURITY` le fait obéir aux politiques sur
  les lignes, mais un propriétaire garde `TRUNCATE` (aucune politique n'est
  jamais consultée pour TRUNCATE), `LOCK`, `DROP TABLE` et `ALTER TABLE …
  DISABLE ROW LEVEL SECURITY` — le mur tombe en une instruction. Ces droits ne
  se révoquent pas, et un event trigger qui les refuserait exige un
  superutilisateur, que CNPG ne donne pas : **il n'existe pas de demi-mesure
  côté base**. Ce qui reste : `TestEveryQueryOnAWalledTableNamesTheCampaign`
  refuse ces quatre verbes contre une table murée **dans le source**, donc le
  chemin « quelqu'un l'écrit » est fermé ; le chemin « quelqu'un obtient du SQL
  arbitraire » ne l'est que par l'absence d'injection. Séparer propriétaire et
  applicatif est le seul vrai correctif, et il a été écarté pour ne provisionner
  qu'un rôle.
