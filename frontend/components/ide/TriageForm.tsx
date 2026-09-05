"use client";

import { useEffect, useState } from "react";
import { triageApi } from "@/lib/api";

/**
 * Play Billing cutover triage, inline in the IDE.
 *
 * One structured record per project: the cutover decision, the date, and the
 * dollar exposure. Dollars are entered as dollars and sent as integer cents,
 * so what the form shows is what the server stores, exactly. Saving revises
 * the record in place — triage never forks.
 */
const DECISIONS = [
  { value: "", label: "Undecided" },
  { value: "cutover_now", label: "Cut over now" },
  { value: "cutover_scheduled", label: "Cut over on a date" },
  { value: "stay_simulator", label: "Stay on simulator billing" },
];

export default function TriageForm({ projectId }: { projectId: number }) {
  const [decision, setDecision] = useState("");
  const [date, setDate] = useState("");
  const [dollars, setDollars] = useState("");
  const [notes, setNotes] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savedTick, setSavedTick] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setLoaded(false);
    triageApi
      .get(projectId)
      .then((r) => {
        if (r) {
          setDecision(r.decision);
          setDate(r.cutover_date);
          setDollars(r.exposure_cents ? (r.exposure_cents / 100).toFixed(2) : "");
          setNotes(r.notes);
        }
      })
      .catch(() => {})
      .finally(() => setLoaded(true));
  }, [projectId]);

  async function save() {
    if (!decision.trim()) {
      setError("Pick a decision first.");
      return;
    }
    const cents = dollars.trim() === "" ? 0 : Math.round(parseFloat(dollars) * 100);
    if (Number.isNaN(cents) || cents < 0) {
      setError("Exposure must be a dollar amount, e.g. 199.99.");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const r = await triageApi.save(projectId, {
        decision: decision.trim(),
        cutover_date: date.trim(),
        exposure_cents: cents,
        notes,
      });
      setDecision(r.decision);
      setDate(r.cutover_date);
      setDollars(r.exposure_cents ? (r.exposure_cents / 100).toFixed(2) : "");
      setNotes(r.notes);
      setSavedTick(true);
      setTimeout(() => setSavedTick(false), 2000);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save.");
    } finally {
      setSaving(false);
    }
  }

  if (!loaded) {
    return <p className="p-3 font-mono text-xs text-ash">Loading triage…</p>;
  }

  return (
    <div className="flex flex-col gap-3 p-3">
      <div>
        <p className="gauge mb-1">Billing cutover triage</p>
        <p className="text-sm text-mist">
          When this app takes real money, and what that exposes. Recorded once,
          revised in place.
        </p>
      </div>

      <label className="block">
        <span className="gauge">Decision</span>
        <select value={decision} onChange={(e) => setDecision(e.target.value)} className="field mt-1">
          {DECISIONS.map((d) => (
            <option key={d.value} value={d.value}>
              {d.label}
            </option>
          ))}
        </select>
      </label>

      <div className="flex gap-2">
        <label className="block flex-1">
          <span className="gauge">Cutover date</span>
          <input
            type="date"
            value={date}
            onChange={(e) => setDate(e.target.value)}
            className="field mt-1"
          />
        </label>
        <label className="block flex-1">
          <span className="gauge">Exposure ($)</span>
          <input
            type="text"
            inputMode="decimal"
            value={dollars}
            onChange={(e) => setDollars(e.target.value)}
            placeholder="199.99"
            className="field mt-1 font-mono"
          />
        </label>
      </div>

      <label className="block">
        <span className="gauge">Notes</span>
        <textarea
          rows={3}
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          placeholder="Holiday freeze, pending pricing review…"
          className="field mt-1 resize-none leading-snug"
        />
      </label>

      {error && <p className="text-xs text-coral-red">{error}</p>}
      <button type="button" onClick={save} disabled={saving} className="btn btn-go w-full">
        {saving ? "Saving…" : savedTick ? "Saved ✓" : "Save triage"}
      </button>
    </div>
  );
}
