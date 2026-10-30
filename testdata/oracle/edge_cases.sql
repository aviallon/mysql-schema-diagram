-- Fixture de cas limites : constructions que le dump réel n'utilise pas, mais
-- qu'un dump MySQL 5.7 / 8.x peut contenir. L'oracle associé est produit par un
-- vrai serveur MariaDB (voir regenerate-oracle.sh) : c'est lui qui fait foi,
-- jamais une valeur recopiée à la main depuis la documentation.
SET SQL_MODE = "NO_AUTO_VALUE_ON_ZERO";

-- --------------------------------------------------------
-- Table sans clé primaire, avec une contrainte CHECK.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `plain_registry`;
CREATE TABLE `plain_registry` (
  `code` varchar(20) NOT NULL,
  `label` varchar(100) DEFAULT NULL,
  `amount` int NOT NULL DEFAULT '0',
  CONSTRAINT `chk_amount` CHECK (`amount` >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- --------------------------------------------------------
-- Types, jeux de caractères, index composites et préfixes, colonnes générées.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_types`;
CREATE TABLE `edge_types` (
  `id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `big` bigint(20) unsigned DEFAULT NULL,
  `zero` int(10) unsigned zerofill DEFAULT NULL,
  `price` decimal(10,2) NOT NULL DEFAULT '0.00',
  `kind` enum('alpha','beta','gamma') NOT NULL DEFAULT 'alpha',
  `flags` set('x','y','z') DEFAULT NULL,
  `tiny_flag` tinyint(1) NOT NULL DEFAULT '0',
  `wide_flag` tinyint(4) NOT NULL DEFAULT '0',
  `body` text,
  `when_at` datetime NOT NULL DEFAULT '2000-01-01 00:00:00',
  `created` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `counter` int(11) NOT NULL DEFAULT '0',
  `total` int(11) GENERATED ALWAYS AS (`counter` * 2) STORED,
  `latin_name` varchar(40) CHARACTER SET latin1 COLLATE latin1_swedish_ci DEFAULT NULL,
  `payload` longtext CHARACTER SET utf8 COLLATE utf8_bin,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_big_kind` (`big`,`kind`),
  KEY `idx_kind_big` (`kind`,`big`),
  KEY `idx_latin_prefix` (`latin_name`(10)),
  FULLTEXT KEY `ft_body` (`body`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='cas limites';

-- --------------------------------------------------------
-- Table sans clé primaire, sans contrainte.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_nopk`;
CREATE TABLE `edge_nopk` (
  `a` int(11) NOT NULL,
  `b` varchar(10) NOT NULL
) ENGINE=MyISAM DEFAULT CHARSET=utf8;

-- --------------------------------------------------------
-- Clé étrangère déclarée.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_parent`;
CREATE TABLE `edge_parent` (
  `id` int(11) NOT NULL AUTO_INCREMENT,
  `code` varchar(20) NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_code` (`code`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS `edge_child`;
CREATE TABLE `edge_child` (
  `id` int(11) NOT NULL AUTO_INCREMENT,
  `parent_id` int(11) NOT NULL,
  `parent_code` varchar(20) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `fk_parent` (`parent_id`),
  CONSTRAINT `fk_edge_child_parent` FOREIGN KEY (`parent_id`) REFERENCES `edge_parent` (`id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- --------------------------------------------------------
-- Règle du premier TIMESTAMP.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_timestamps`;
CREATE TABLE `edge_timestamps` (
  `id` int(11) NOT NULL,
  `ts_first` timestamp NOT NULL,
  `ts_nullable` timestamp NULL DEFAULT NULL,
  `ts_explicit` timestamp NOT NULL DEFAULT '2020-01-01 00:00:00',
  `ts_later` timestamp NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- --------------------------------------------------------
-- ALTER TABLE : colonne et index ajoutés après coup.
-- --------------------------------------------------------
ALTER TABLE `edge_child`
  ADD COLUMN `note` varchar(50) DEFAULT NULL,
  ADD KEY `idx_note` (`note`(5));

-- --------------------------------------------------------
-- Convention « id_<table> » et « fk_id_<table> » : la table cible se lit dans
-- le nom de la colonne. Ces deux tables sont volontairement génériques : aucune
-- base réelle n'est citée dans ce dépôt.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `catalogue`;
CREATE TABLE `catalogue` (
  `id_catalogue` int(11) NOT NULL AUTO_INCREMENT,
  `libelle` varchar(40) NOT NULL,
  PRIMARY KEY (`id_catalogue`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS `article`;
CREATE TABLE `article` (
  `id_article` int(11) NOT NULL AUTO_INCREMENT,
  `id_catalogue` int(11) NOT NULL,
  `fk_id_catalogue` int(11) DEFAULT NULL,
  `ref` varchar(20) DEFAULT NULL,
  PRIMARY KEY (`id_article`),
  KEY `k_catalogue` (`id_catalogue`),
  KEY `k_fk_catalogue` (`fk_id_catalogue`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- --------------------------------------------------------
-- Vue écrite comme mysql/mariadb-dump le fait : encapsulée dans un commentaire
-- exécutable. La supprimer faisait disparaître 100 % des vues d'un dump réel.
-- --------------------------------------------------------
/*!50001 CREATE VIEW `edge_view_commented` AS SELECT `id`, `code` FROM `edge_parent` */;

-- --------------------------------------------------------
-- ALTER TABLE … RENAME TO : la table doit survivre au renommage.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_renamed_old`;
CREATE TABLE `edge_renamed_old` (
  `id` int(11) NOT NULL,
  `valeur` int(11) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
ALTER TABLE `edge_renamed_old` RENAME TO `edge_renamed_new`;

-- --------------------------------------------------------
-- Règle du premier TIMESTAMP, dans ses trois variantes.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_ts_null_first`;
CREATE TABLE `edge_ts_null_first` (
  `id` int(11) NOT NULL,
  `a` timestamp NULL DEFAULT NULL,
  `b` timestamp NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS `edge_ts_default_first`;
CREATE TABLE `edge_ts_default_first` (
  `id` int(11) NOT NULL,
  `a` timestamp NOT NULL DEFAULT '2020-01-01 00:00:00',
  `b` timestamp NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS `edge_ts_precision`;
CREATE TABLE `edge_ts_precision` (
  `id` int(11) NOT NULL,
  `ts3` timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- --------------------------------------------------------
-- COLUMN_KEY : promotion d'un unique NOT NULL sans clé primaire, et colonne
-- présente uniquement en position ≥ 2 d'un index composite.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_keys`;
CREATE TABLE `edge_keys` (
  `sn` int(11) NOT NULL,
  `sc` varchar(10) NOT NULL,
  `nn` int(11) DEFAULT NULL,
  `s1` int(11) NOT NULL,
  `s2` int(11) NOT NULL,
  UNIQUE KEY `u_sn` (`sn`),
  UNIQUE KEY `u_sc` (`sc`),
  UNIQUE KEY `u_nn` (`nn`),
  UNIQUE KEY `u_comp` (`s1`,`s2`),
  KEY `k_s2` (`s2`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- --------------------------------------------------------
-- Commentaire de colonne, YEAR(4), COLLATE sans CHARACTER SET.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_charset`;
CREATE TABLE `edge_charset` (
  `id` int(11) NOT NULL,
  `commented` int(11) NOT NULL COMMENT 'commentaire de colonne',
  `annee` year(4) DEFAULT NULL,
  `only_collate` varchar(10) COLLATE utf8mb4_bin DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=latin1;

-- --------------------------------------------------------
-- Ordre réel des colonnes après ADD COLUMN … FIRST / AFTER.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_positions`;
CREATE TABLE `edge_positions` (
  `a` int(11) NOT NULL AUTO_INCREMENT,
  `c` int(11) NOT NULL,
  PRIMARY KEY (`a`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
ALTER TABLE `edge_positions` ADD COLUMN `z` int(11) NOT NULL AFTER `a`;
ALTER TABLE `edge_positions` ADD COLUMN `y` int(11) NOT NULL FIRST;

-- --------------------------------------------------------
-- DROP COLUMN sur un index composite : l'index survit, amputé.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_dropcol`;
CREATE TABLE `edge_dropcol` (
  `a` int(11) NOT NULL,
  `b` int(11) NOT NULL,
  `c` int(11) NOT NULL,
  PRIMARY KEY (`c`),
  KEY `idx_ab` (`a`,`b`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
ALTER TABLE `edge_dropcol` DROP COLUMN `b`;

-- --------------------------------------------------------
-- Clé étrangère anonyme : le serveur la nomme <table>_ibfk_n.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_anon_parent`;
CREATE TABLE `edge_anon_parent` (
  `id` int(11) NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS `edge_anon_child`;
CREATE TABLE `edge_anon_child` (
  `id` int(11) NOT NULL,
  `pid` int(11) NOT NULL,
  PRIMARY KEY (`id`),
  KEY `k_pid` (`pid`),
  FOREIGN KEY (`pid`) REFERENCES `edge_anon_parent` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- --------------------------------------------------------
-- Cloisonnement : physique, mais absent de information_schema.
-- --------------------------------------------------------
DROP TABLE IF EXISTS `edge_partitioned`;
CREATE TABLE `edge_partitioned` (
  `id` int(11) NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
PARTITION BY HASH (`id`) PARTITIONS 4;

-- --------------------------------------------------------
-- Table temporaire : elle n'existe pas dans information_schema.
-- --------------------------------------------------------
DROP TEMPORARY TABLE IF EXISTS `edge_tmp_ghost`;
CREATE TEMPORARY TABLE `edge_tmp_ghost` (
  `id` int(11) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- --------------------------------------------------------
-- Vue.
-- --------------------------------------------------------
DROP VIEW IF EXISTS `edge_view`;
CREATE VIEW `edge_view` AS SELECT `id`, `code` FROM `edge_parent`;
