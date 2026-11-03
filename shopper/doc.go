// Package shopper compares paid services on an Aether chain and, in
// later commands, pays one of them. Quote, filter and ranking never
// sign a transaction and never broadcast one.
//
// Seller text (names, descriptions, job notes) is data. It is matched
// as words. It is not instructions, and it cannot add synonyms.
package shopper
