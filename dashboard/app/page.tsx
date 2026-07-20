import DisruptionsTable from "@/components/DisruptionsTable";
import SubscribeForm from "@/components/SubscribeForm";

export default function Home() {
  return (
    <main>
      <h2>
        Active disruptions{" "}
        <span className="count-note">— live from the city feed, refreshes every 30s</span>
      </h2>
      <DisruptionsTable />

      <h2>Email alerts</h2>
      <SubscribeForm />

      <p className="footer">
        Data: City of Calgary GTFS-Realtime service alerts (open data, polled
        every 30s). This is an independent monitor, not affiliated with Calgary
        Transit. Click a row for full detail; click a route number for its
        history.
      </p>
    </main>
  );
}
