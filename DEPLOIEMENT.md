# Déployer l'app sur un serveur

## En bref

```bash
cp .env.exemple .env            # puis remplir : secrets, mot de passe base,
                                # identité de campagne
task build                      # (re)génère la liste des maires embarquée
docker compose up -d --build    # PostgreSQL + app sur :8047
```

L'application est **sans état** : comptes, affectations, statuts et notes
vivent tous dans PostgreSQL. C'est ce qui permet d'en faire tourner
plusieurs instances — et c'est aussi ce qu'il faut sauvegarder.

Puis un reverse-proxy HTTPS devant. Exemple Caddy (`Caddyfile`) :

```
parrainages.mondomaine.fr {
    reverse_proxy 127.0.0.1:8047
}
```

(Alternative sans domaine : tunnel `cloudflared` ou Tailscale Funnel pointé
sur le port 8047. Alternative sans Docker : `task web-build` puis `task api`
derrière le proxy, avec les mêmes variables d'environnement — l'API sert
elle-même l'interface, il n'y a rien d'autre à installer.)

## Configuration au runtime (variables d'environnement)

Toutes les valeurs de `config/campagne.toml` sont surchargeables par des
variables `PARAPHE_*` — c'est la voie recommandée sur serveur (voir la liste
complète et les exemples dans `.env.exemple`) :

| Variable | Rôle |
|---|---|
| `PARAPHE_CANDIDATE` | nom du candidat (apparaît dans tous les messages) |
| `PARAPHE_CANDIDATE_DESCRIPTION` | une ligne factuelle (email, script d'appel) |
| `PARAPHE_CANDIDATE_DESCRIPTION_LONG` | 2-3 phrases à la 1ʳᵉ personne (courrier) |
| `PARAPHE_SIGNATORY` / `_QUALITE` | signature par défaut (publipostage de masse) |
| `PARAPHE_CONTACT_PHONE` / `_EMAIL` / `PARAPHE_SITE` | coordonnées de la campagne |
| `PARAPHE_SENDING_CITY` | lieu daté du courrier |
| `PARAPHE_BATCH_SIZE` | maires attribués par « prendre un lot » (défaut 10) |
| `PARAPHE_ADMIN_EMAIL` / `_PASSWORD` / `_NOM` | compte de coordination, créé ou remis à jour à chaque démarrage — **obligatoire au premier lancement** |
| `PARAPHE_SECRET_KEY` | secret de signature des sessions (32+ octets aléatoires) |
| `PARAPHE_HTTPS` | `1` derrière un proxy HTTPS : pose le cookie `Secure` |
| `PARAPHE_SOURCE_URL` | URL du dépôt public : affiche « code source » en pied de page |
| `PARAPHE_DATABASE_URL` | DSN PostgreSQL — **obligatoire**, l'app refuse de démarrer sans |
| `PARAPHE_HOST` / `PARAPHE_PORT` | interface et port d'écoute |
| `PARAPHE_BASE_DOMAIN` | domaine des sous-domaines de campagne — **vide = une seule campagne** (voir plus bas) |
| `PARAPHE_ORG_SLUG` | sous-domaine de la campagne décrite par les variables ci-dessus (défaut `campagne`) |
| `PARAPHE_INSTANCE_ADMIN_EMAIL` / `_PASSWORD` / `_NOM` | administration de l'instance : valide les demandes d'hébergement. Requis en multi-campagnes |

## Comptes, équipes locales et cloisonnement

L'accès se fait par **compte individuel** (email + mot de passe), pas par code
partagé — pour savoir qui a contacté qui, et pouvoir couper un accès sans
perturber les autres.

- Au premier démarrage, `PARAPHE_ADMIN_EMAIL` + `PARAPHE_ADMIN_PASSWORD`
  créent le compte de **coordination**. Sans eux, personne ne peut entrer et
  l'app le dit clairement au lieu de s'ouvrir.
- La coordination crée les **équipes** (un nom, des départements) et leurs
  **référents**. Chaque référent ouvre ensuite les accès de ses bénévoles :
  l'app génère un mot de passe provisoire, affiché **une seule fois**.
- Un référent ne peut créer que des bénévoles, et seulement dans son équipe
  (toute tentative d'élever le rôle ou de changer d'équipe est ignorée côté
  serveur).
- **Cloisonnement** : une équipe voit ses propres réservations et le vivier
  libre ; les fiches réservées par une autre équipe lui sont refusées (403).
  Les compteurs de campagne (statuts, départements couverts) restent visibles
  par tous, **sans aucun nom**.
- Désactiver un compte prend effet à la requête suivante, sans attendre la
  déconnexion.

Une variable absente = valeur du `campagne.toml` embarqué (ou de
`config/campagne.local.toml`, ignoré par git, si vous préférez un fichier).

Deux comportements distincts, volontairement :
- un **secret** resté à la valeur d'exemple du dépôt (`PARAPHE_SECRET_KEY`,
  `PARAPHE_ADMIN_PASSWORD`) → **refus de démarrer** : ces valeurs sont
  publiques, les accepter livrerait une instance ouverte ;
- une **valeur de campagne** non remplie → l'app démarre et affiche un
  bandeau d'avertissement sur chaque page, mais le publipostage de masse
  refuse de tourner. On peut donc explorer l'outil avant de le configurer.

`PARAPHE_HTTPS` : toute valeur non vide active le cookie `Secure`. Pour le
désactiver (dev en HTTP), laisser la variable **absente** — `0` l'active.

## Données et sauvegarde

- La liste des maires est **dans l'image** (figée au build) ; le travail de
  l'équipe (comptes, attributions, statuts, notes) est dans PostgreSQL.
  **C'est la seule donnée irremplaçable.** En Docker :
  `docker compose exec postgres pg_dump -U paraphe paraphe > backup-$(date +%F).sql`
  En Kubernetes, activez `postgres.cnpg.backup.*` : le chart pose alors
  l'archivage WAL **et** un `ScheduledBackup` quotidien. Les deux sont
  nécessaires — l'archivage WAL seul ne permet aucune restauration.
- Mise à jour de la liste (nouveau `task build`) : `docker compose up -d
  --build` ré-importe en UPSERT : les données (email, rang, score) sont
  rafraîchies, les colonnes de travail (bénévole, statut, notes) intactes.
  Une cible retirée de la liste est supprimée si personne ne l'a travaillée,
  signalée « RETIRÉ » sinon.

## Plusieurs campagnes sur la même instance

Une instance peut héberger **plusieurs campagnes**, chacune sur son
sous-domaine. Il suffit de renseigner `PARAPHE_BASE_DOMAIN` :

```
PARAPHE_BASE_DOMAIN=paraphe.fr
```

Dès lors :

- `paraphe.fr` sert l'**accueil de l'instance** : formulaire public de demande
  d'hébergement, et connexion de l'administration ;
- `<campagne>.paraphe.fr` sert **une campagne** — ses équipes, son travail,
  sa configuration, invisibles des autres ;
- une demande ne crée **rien** : un administrateur d'instance la valide, et
  c'est la validation qui ouvre la campagne et rend, une seule fois, le mot de
  passe de sa coordination. Sans cette modération, le premier abus est le
  squat du nom d'un candidat, sans recours pour la campagne squattée ;
- la coordination de chaque campagne renseigne sa configuration dans
  l'application (onglet « Mon équipe ») : les `PARAPHE_*` de campagne
  n'amorcent plus que la PREMIÈRE.

**Laisser `PARAPHE_BASE_DOMAIN` vide reste le mode par défaut** : tout nom
d'hôte sert alors l'unique campagne configurée, sans DNS particulier.

### Rôle PostgreSQL — sans privilège, obligatoirement

La cloison entre campagnes est appliquée par PostgreSQL (`ROW LEVEL SECURITY`,
en mode `FORCE`), pas par les clauses `WHERE` de l'application. Or **un
superutilisateur — et tout rôle `BYPASSRLS` — traverse ces politiques sans les
voir** : l'application tournerait comme si le cloisonnement n'existait pas, et
la première clause fausse enverrait le travail d'une campagne chez une autre,
sans un signe.

C'est le défaut par défaut : l'image officielle de PostgreSQL fait du compte
`POSTGRES_USER` un superutilisateur. **L'application refuse donc de démarrer**
en multi-campagnes si son rôle est privilégié (en mono-campagne, elle se
contente de l'avertir). Créez un rôle dédié :

```sql
CREATE ROLE paraphe_app LOGIN PASSWORD '…' NOSUPERUSER NOBYPASSRLS;
GRANT CREATE, USAGE ON SCHEMA public TO paraphe_app;
```

puis pointez `PARAPHE_DATABASE_URL` sur ce rôle. Avec CloudNativePG, le secret
`<cluster>-app` généré par l'opérateur convient déjà : son rôle n'est pas
superutilisateur.

#### Ce que ce rôle peut encore faire, et pourquoi c'est assumé

Ce rôle est **propriétaire** des tables — il faut bien que quelqu'un les crée,
et l'application fait son propre schéma au démarrage. `FORCE ROW LEVEL
SECURITY` le fait obéir aux politiques ligne à ligne, mais un propriétaire
garde quatre pouvoirs qu'aucune politique ne borne :

- `TRUNCATE` — une politique n'est **jamais** consultée pour TRUNCATE ;
- `LOCK TABLE … ACCESS EXCLUSIVE` — ne visite aucune ligne ;
- `DROP TABLE` ;
- `ALTER TABLE … DISABLE ROW LEVEL SECURITY` — le mur tombe en une instruction.

Ces droits ne se révoquent pas au propriétaire, et un event trigger qui les
refuserait exige un superutilisateur, que CloudNativePG ne fournit pas. Le seul
correctif réel serait deux rôles — un propriétaire pour le schéma, un rôle
d'exécution qui ne reçoit que `SELECT, INSERT, UPDATE, DELETE` — au prix d'un
second rôle à provisionner et d'une étape de migration séparée. **Choix
assumé : un seul rôle.**

Ce qui reste en face : la suite de tests refuse ces quatre verbes contre une
table cloisonnée **dans le code source**, donc personne ne les écrit par
inadvertance. Le risque résiduel est un attaquant capable d'exécuter du SQL
arbitraire — exactement la situation où RLS devrait le contenir, et où il
pourrait ici s'en affranchir. À rouvrir si l'instance héberge un jour des
campagnes rivales.

### Kubernetes + HAProxy Ingress

Le chart rend l'Ingress avec **deux hôtes** dès que `instance.baseDomain` est
renseigné : l'apex, et l'hôte générique `*.<domaine>`. C'est le générique qui
permet d'ouvrir une campagne sans toucher au cluster — sinon chaque validation
exigerait de modifier l'Ingress, donc de donner à l'application un accès en
écriture à l'API Kubernetes.

```bash
helm upgrade --install paraphe chart/paraphe \
  --set ingress.enabled=true --set ingress.className=haproxy \
  --set ingress.tls.enabled=true \
  --set ingress.host=paraphe.fr \
  --set instance.baseDomain=paraphe.fr \
  --set instance.admin.email=admin@paraphe.fr \
  --set secrets.instanceAdminPassword="$(openssl rand -hex 24)" \
  --set secrets.adminPassword="$(openssl rand -hex 24)" \
  --set secrets.secretKey="$(openssl rand -hex 32)"
```

Côté infrastructure :

- **DNS** : `paraphe.fr` ET `*.paraphe.fr` vers l'IP publique du cluster
  (celle du Service de HAProxy Ingress). Sans l'enregistrement générique,
  chaque campagne ouverte serait injoignable.
- **Certificat GÉNÉRIQUE** : Let's Encrypt ne signe un `*` qu'en **DNS-01** —
  HTTP-01 en est incapable. Il faut donc un `ClusterIssuer` cert-manager avec
  le jeton d'API du fournisseur DNS, puis l'annotation correspondante sur
  l'Ingress (`ingress.annotations`). Un certificat par campagne, délivré à la
  volée, supposerait de créer une ressource Ingress à chaque validation :
  même problème que ci-dessus.
- L'hôte générique de l'API Ingress ne couvre **qu'un niveau**
  (`a.paraphe.fr`, pas `a.b.paraphe.fr`). L'application applique la même
  règle, pour que les deux ne divergent jamais.
- Les sondes interrogent `/health/db`, qui ne résout aucune campagne :
  kubelet s'adresse à l'IP du pod, dont le nom d'hôte ne correspond à aucun
  sous-domaine. Une sonde sur `/api/config` laisserait tous les pods
  éternellement « non prêts ».

### Ce que voit qui

- Une campagne ne voit **que** son travail : ses équipes, ses comptes, ses
  notes, ses réservations. La liste des maires, elle, est **commune et en
  lecture seule** — c'est une donnée publique, identique pour tous, et la
  dupliquer par campagne reviendrait à recopier 34 826 lignes pour rien.
- L'administration d'instance ne lit **aucune** donnée de campagne : son
  périmètre est distinct, et les politiques de cloisonnement ne lui laissent
  rien passer. Elle voit les demandes et la liste des campagnes, pas les
  notes des bénévoles.
- Une session vaut pour **une** campagne : le cookie porte son identifiant et
  est refusé ailleurs.

## Sécurité, en face de quoi on est

Comptes individuels, mots de passe hashés (werkzeug), cloisonnement par
équipe : adapté à une campagne militante, pas à un service public ouvert. Le
HTTPS du proxy est indispensable — les mots de passe circulent au login.

Les données maires sont **publiques** (Conseil constitutionnel, RNE, annuaire
de l'administration). Ce qui est sensible, ce sont **les notes de l'équipe** :
qui a dit quoi, qui hésite, qui a refusé. D'où : HTTPS, comptes nominatifs,
sauvegardes chiffrées si elles quittent le serveur, et suppression des notes
en fin de campagne.

## Données personnelles

Ce dépôt outille un traitement de données personnelles : nom, prénom,
civilité, commune et coordonnées de mairie de 34 826 personnes identifiées,
plus les notes que l'équipe prend sur elles. **L'équipe qui déploie est
responsable de traitement** — pas l'auteur du dépôt.

- **Base légale** : intérêt légitime (art. 6.1.f du RGPD), communication
  politique vers des élus dans l'exercice de leur mandat, à partir de
  publications officielles.
- **Information des personnes** (art. 14) : les modèles indiquent l'origine
  des données dès le premier contact, et le guide impose de répondre
  précisément si un élu la demande.
- **Droit d'opposition** (art. 21) : c'est le statut « ne plus contacter ».
  Il est définitif et l'équipe ne doit jamais le contourner.
- **Durée** : les notes n'ont d'utilité que pendant la campagne.
  `task purge-notes` les efface et remet les affectations à zéro. À lancer
  après la publication des parrainages par le Conseil constitutionnel.
- **Ce qui est sensible**, ce ne sont pas les coordonnées (publiques) mais
  les notes : qui hésite, qui a refusé, ce qu'un maire a confié. D'où le
  cloisonnement par équipe, le HTTPS et les sauvegardes chiffrées.

## Pré-remplir la version navigateur (facultatif)

Les bénévoles qui travaillent sans compte, dans leur navigateur, retapent
sinon les neuf champs de la campagne — et une faute de frappe part aux
maires sous le nom de la campagne. Construite avec `PARAPHE_BASE_DOMAIN`,
l'interface accepte `?org=<slug>` et propose de reprendre la campagne
publiée par `<slug>.<domaine>` :

```
PARAPHE_BASE_DOMAIN=paraphe.fr PARAPHE_BASE_PATH=/paraphe/ pnpm --dir web build
```

Ce que ça expose : `GET /api/campaign/public` rend le slug, le nom et les
neuf clés de campagne — rien d'autre, et avec `Access-Control-Allow-Origin: *`
puisque ce sont exactement les valeurs qui partent déjà dans chaque message
à un maire. Aucune session, aucun cookie.

**Le paramètre nomme une CAMPAGNE, jamais un hôte** : le domaine est figé à
la construction. Un lien forgé ne peut donc pas glisser les coordonnées d'un
tiers sous le nom d'un vrai candidat — il faudrait pour cela faire approuver
une campagne sur votre instance. Le bénévole voit de toute façon les valeurs
avant d'accepter, et une campagne encore au gabarit ne pré-remplit rien
(409).

Laissez `PARAPHE_BASE_DOMAIN` vide à la construction et `?org=` ne fait
rien du tout.
