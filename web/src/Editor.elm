module Editor exposing (Effect(..), Model, Msg(..), init, pipeline, update, view)

import Api exposing (Region, Sermon, Timeline, Waveform)
import Html exposing (Html, audio, button, canvas, div, h1, h2, h3, p, span, strong, text)
import Html.Attributes exposing (attribute, class, controls, disabled, id, src, tabindex)
import Html.Events exposing (onClick)
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import Timeline
import Ui


type alias Model =
    { sermon : Sermon
    , waveform : Maybe Waveform
    , analysis : Maybe Timeline
    , draft : Maybe Timeline
    , draftReceived : Bool
    , regions : List Region
    , selected : Int
    , dirty : Bool
    , applying : Bool
    , finalReady : Bool
    , confirmingApproval : Bool
    , error : Maybe String
    , backWarning : Bool
    , playhead : Float
    , zoom : Float
    , viewStart : Float
    }


type Msg
    = GotWaveform (Result Http.Error Waveform)
    | GotAnalysis (Result Http.Error Timeline)
    | GotDraft Decode.Value
    | Select Int
    | Toggle Int
    | Nudge Int Float
    | Boundary Decode.Value
    | Play Int
    | Playhead Decode.Value
    | ZoomIn
    | ZoomOut
    | Pan Float
    | ShowAll
    | Apply
    | Applied (Result Http.Error Sermon)
    | ConfirmApproval
    | CancelApproval
    | Approve
    | Approved (Result Http.Error Sermon)
    | Back
    | ForceClose
    | KeepEditing


type Effect
    = None
    | LoadDraft String
    | SaveDraft String Encode.Value Encode.Value Encode.Value
    | ClearDraft String
    | Render Encode.Value Encode.Value
    | Complete String Encode.Value Encode.Value
    | Preview Encode.Value
    | PlayRegion Float Float
    | Close
    | ApprovedSermon Sermon


init : Sermon -> ( Model, Cmd Msg, Effect )
init sermon =
    ( { sermon = sermon, waveform = Nothing, analysis = Nothing, draft = Nothing, draftReceived = False, regions = [], selected = 0, dirty = False, applying = False, finalReady = sermon.stage == "edit" && sermon.status == "done", confirmingApproval = False, error = Nothing, backWarning = False, playhead = 0, zoom = 1, viewStart = 0 }
    , Cmd.batch [ Api.waveform GotWaveform sermon.id, Api.analyze GotAnalysis sermon.id ]
    , LoadDraft sermon.id
    )


resolve : Model -> Model
resolve model =
    case ( model.waveform, model.analysis, model.draftReceived ) of
        ( Just wave, Just analyzed, True ) ->
            let
                validDraft =
                    model.draft
                        |> Maybe.andThen
                            (\draft ->
                                if abs (draft.duration - wave.duration) < 0.01 && valid wave.duration draft.regions then
                                    Just draft.regions

                                else
                                    Nothing
                            )

                chosen =
                    validDraft
                        |> Maybe.withDefault
                            (if model.finalReady then
                                model.sermon.appliedRegions |> Maybe.withDefault analyzed.regions

                             else
                                analyzed.regions
                            )
            in
            if List.isEmpty model.regions then
                { model | regions = chosen, dirty = validDraft /= Nothing, finalReady = model.finalReady && validDraft == Nothing }

            else
                model

        _ ->
            model


update : Msg -> Model -> ( Model, Cmd Msg, Effect )
update msg model =
    case msg of
        GotWaveform result ->
            case result of
                Ok wave ->
                    finish { model | waveform = Just wave }

                Err _ ->
                    ( { model | error = Just "Could not load the waveform. Please return and try again." }, Cmd.none, None )

        GotAnalysis result ->
            case result of
                Ok value ->
                    finish { model | analysis = Just value }

                Err _ ->
                    ( { model | error = Just "Could not analyze this recording. Please try again." }, Cmd.none, None )

        GotDraft value ->
            case Decode.decodeValue (Decode.map2 Tuple.pair (Decode.field "id" Decode.string) (Decode.field "draft" Decode.value)) value of
                Ok ( id, draftValue ) ->
                    if id == model.sermon.id then
                        case Decode.decodeValue (Decode.nullable Timeline.decoder) draftValue of
                            Ok draft ->
                                finish { model | draftReceived = True, draft = draft }

                            Err _ ->
                                finishDiscardDraft { model | draftReceived = True, draft = Nothing }

                    else
                        ( model, Cmd.none, None )

                Err _ ->
                    ( model, Cmd.none, None )

        Select index ->
            ( { model | selected = index }, Cmd.none, None )

        Toggle index ->
            if busy model then
                ( model, Cmd.none, None )

            else
                changed
                    { model
                        | regions =
                            List.indexedMap
                                (\i r ->
                                    if i == index then
                                        { r | keep = not r.keep }

                                    else
                                        r
                                )
                                model.regions
                        , selected = index
                    }

        Nudge boundary delta ->
            if busy model then
                ( model, Cmd.none, None )

            else
                changeBoundary boundary (Timeline.boundaryTime boundary model.regions + delta) model

        Boundary value ->
            case Decode.decodeValue (Decode.map2 Tuple.pair (Decode.field "boundary" Decode.int) (Decode.field "time" Decode.float)) value of
                Ok ( boundary, time ) ->
                    if busy model then
                        ( model, Cmd.none, None )

                    else
                        changeBoundary boundary time model

                Err _ ->
                    ( model, Cmd.none, None )

        Play index ->
            case get index model.regions of
                Just region ->
                    ( { model | selected = index }, Cmd.none, PlayRegion region.start region.end )

                Nothing ->
                    ( model, Cmd.none, None )

        Playhead value ->
            case Decode.decodeValue Decode.float value of
                Ok time ->
                    ( { model | playhead = time }, Cmd.none, None )

                Err _ ->
                    ( model, Cmd.none, None )

        ZoomIn ->
            redraw (setZoom (model.zoom * 2) model)

        ZoomOut ->
            redraw (setZoom (model.zoom / 2) model)

        Pan direction ->
            let
                span =
                    visibleSpan model

                next =
                    { model | viewStart = clampViewStart model (model.viewStart + direction * span * 0.75) }
            in
            redraw next

        ShowAll ->
            redraw { model | zoom = 1, viewStart = 0 }

        Apply ->
            if busy model then
                ( model, Cmd.none, None )

            else
                ( { model | applying = True, error = Nothing }, Api.applyEdits Applied model.sermon.id model.regions, None )

        Applied result ->
            case result of
                Ok _ ->
                    -- SSE snapshots and updates are authoritative. The queue can
                    -- finish before this older HTTP acknowledgement arrives.
                    ( model, Cmd.none, None )

                Err _ ->
                    ( { model | applying = False, error = Just "Could not queue the edit. Check the regions and try again." }, Cmd.none, None )

        ConfirmApproval ->
            ( { model | confirmingApproval = True }, Cmd.none, None )

        CancelApproval ->
            ( { model | confirmingApproval = False }, Cmd.none, None )

        Approve ->
            ( { model | applying = True }, Api.approveEdit Approved model.sermon.id, None )

        Approved result ->
            case result of
                Ok sermon ->
                    ( model, Cmd.none, ApprovedSermon sermon )

                Err _ ->
                    ( { model | applying = False, confirmingApproval = False, error = Just "Could not approve the final audio. Please try again." }, Cmd.none, None )

        Back ->
            if busy model then
                ( model, Cmd.none, None )

            else if model.dirty then
                ( { model | backWarning = True }, Cmd.none, None )

            else
                ( model, Cmd.none, Close )

        ForceClose ->
            ( model, Cmd.none, Close )

        KeepEditing ->
            ( { model | backWarning = False }, Cmd.none, None )


finish model =
    let
        ready =
            resolve model
    in
    ( ready
    , Cmd.none
    , if List.isEmpty ready.regions then
        None

      else
        renderEffect ready
    )


finishDiscardDraft model =
    let
        ( ready, command, effect ) =
            finish model
    in
    case effect of
        Render render preview ->
            ( ready, command, Complete model.sermon.id render preview )

        _ ->
            ( ready, command, ClearDraft model.sermon.id )


changed model =
    let
        next =
            { model | dirty = True, finalReady = False, error = Nothing }
    in
    case renderEffect next of
        Render render preview ->
            ( next, Cmd.none, SaveDraft next.sermon.id (encodeTimeline next) render preview )

        _ ->
            ( next, Cmd.none, SaveDraft next.sermon.id (encodeTimeline next) Encode.null (previewValue next) )


changeBoundary boundary time model =
    changed { model | regions = Timeline.changeBoundary boundary time model.regions }


pipeline sermon model =
    if sermon.id /= model.sermon.id then
        ( model, None )

    else if sermon.editApproved then
        ( model, ApprovedSermon sermon )

    else if sermon.stage == "edit" && sermon.status == "done" then
        case sermon.appliedRegions of
            Just regions ->
                if model.waveform == Nothing || model.analysis == Nothing || not model.draftReceived then
                    -- Resolve the local draft before deciding whether this is a
                    -- new completion or a reconnect describing an older render.
                    ( { model | sermon = sermon, applying = False, finalReady = True }, None )

                else if model.dirty && model.regions /= regions then
                    -- A reconnect snapshot describes the last successful render,
                    -- not necessarily the user's newer local draft.
                    ( { model | sermon = sermon, applying = False }, None )

                else
                    let
                        next =
                            { model | sermon = sermon, regions = regions, applying = False, finalReady = True, dirty = False, error = Nothing }
                    in
                    case renderEffect next of
                        Render render preview ->
                            ( next, Complete sermon.id render preview )

                        _ ->
                            ( next, ClearDraft sermon.id )

            Nothing ->
                ( { model | sermon = sermon, applying = False, error = Just "The completed edit did not include its persisted regions." }, None )

    else if sermon.stage == "edit" && sermon.status == "failed" then
        ( { model | sermon = sermon, applying = False, error = sermon.error }, None )

    else
        ( { model | sermon = sermon, applying = sermon.stage == "edit" && (sermon.status == "pending" || sermon.status == "running") }, None )


valid duration regions =
    Timeline.valid duration regions


get index list =
    List.drop index list |> List.head


encodeRegions =
    Timeline.encodeRegions


encodeTimeline model =
    Encode.object [ ( "duration", Encode.float (model.waveform |> Maybe.map .duration |> Maybe.withDefault 0) ), ( "regions", encodeRegions model.regions ) ]


previewValue model =
    Encode.object
        [ ( "mode"
          , Encode.string
                (if model.finalReady then
                    "final"

                 else
                    "preview"
                )
          )
        , ( "regions", encodeRegions model.regions )
        ]


renderEffect model =
    case model.waveform of
        Just wave ->
            Render
                (Encode.object
                    [ ( "duration", Encode.float wave.duration )
                    , ( "samples", Encode.list Encode.float wave.samples )
                    , ( "regions", encodeRegions model.regions )
                    , ( "playhead", Encode.float model.playhead )
                    , ( "view_start", Encode.float model.viewStart )
                    , ( "view_end", Encode.float (model.viewStart + visibleSpan model) )
                    ]
                )
                (previewValue model)

        Nothing ->
            None


view model =
    div [ class "editor" ] [ h1 [] [ text "Automatic Audio Timeline" ], p [ class "editor__filename" ] [ text model.sermon.originalFilename ], viewBody model ]


viewBody model =
    if loadFailed model then
        div [ class "editor__load-error" ]
            [ viewError model.error
            , button [ Ui.button, onClick ForceClose ] [ text "Back to Sermons" ]
            ]

    else if model.waveform == Nothing || model.analysis == Nothing || not model.draftReceived then
        p [ class "editor__loading" ] [ text "Loading waveform and finding sections…" ]

    else
        div []
            [ viewError model.error
            , canvas
                [ id "timeline-canvas"
                , class "timeline focusable"
                , attribute "role" "img"
                , attribute "aria-label" "Audio waveform with editable regions"
                , attribute "data-busy"
                    (if busy model then
                        "true"

                     else
                        "false"
                    )
                , tabindex 0
                ]
                []
            , viewTimelineControls model
            , p [ class "timeline-key" ] [ text "Green: speaking · Red: singing · Gray: silence. Hatched sections will be deleted." ]
            , audio [ id "timeline-audio", class "editor__audio", controls True, src (audioSource model) ] []
            , viewStatus model
            , div [ class "editor__actions" ]
                [ button [ Ui.primaryButton, onClick Apply, disabled (busy model || model.finalReady || not (validPlan model)) ]
                    [ text
                        (if model.applying then
                            "Rendering…"

                         else
                            "Apply Edits"
                        )
                    ]
                , if model.finalReady then
                    button [ Ui.dangerButton, onClick ConfirmApproval, disabled model.applying ] [ text "Approve Final Audio" ]

                  else
                    text ""
                , button [ Ui.button, onClick Back, disabled (busy model) ] [ text "Back to Sermons" ]
                ]
            , approval model
            , backWarning model
            , h2 [] [ text "Sections" ]
            , div [ class "region-list" ] (List.indexedMap (regionCard model) model.regions)
            ]


validPlan model =
    valid (model.waveform |> Maybe.map .duration |> Maybe.withDefault 0) model.regions && List.any .keep model.regions


audioSource model =
    "/api/sermons/"
        ++ model.sermon.id
        ++ "/audio/"
        ++ (if model.finalReady then
                "final?v=" ++ String.fromInt model.sermon.progress

            else
                "proxy?gate=" ++ String.fromInt model.sermon.normalizationGateAdjustment ++ "&volume=" ++ String.fromInt model.sermon.normalizationVolumeAdjustment
           )


viewStatus model =
    if model.applying then
        p [ class "editor__status" ] [ text ("Rendering final audio… " ++ String.fromInt model.sermon.progress ++ "%") ]

    else if model.finalReady then
        p [ class "editor__status" ] [ strong [] [ text "Final audio is ready. Listen carefully, then approve or adjust sections and apply again." ] ]

    else
        text ""


viewError error =
    error |> Maybe.map (\e -> p [ Ui.errorText ] [ text e ]) |> Maybe.withDefault (text "")


approval model =
    if model.confirmingApproval then
        div [ Ui.confirmBox ] [ p [ Ui.confirmBoxQuestion ] [ strong [] [ text "This permanently deletes the raw recording. You won't be able to re-edit this sermon. Approve?" ] ], div [ Ui.confirmBoxButtons ] [ button [ Ui.dangerButton, onClick Approve, disabled model.applying ] [ text "Yes, Approve Permanently" ], button [ Ui.button, onClick CancelApproval ] [ text "Cancel" ] ] ]

    else
        text ""


backWarning model =
    if model.backWarning then
        div [ class "editor-warning" ] [ p [] [ strong [] [ text "You have unapplied changes. Your draft is saved in this browser." ] ], button [ Ui.button, onClick ForceClose ] [ text "Leave Editor" ], button [ Ui.button, onClick KeepEditing ] [ text "Keep Editing" ] ]

    else
        text ""


regionCard model index region =
    div
        [ class
            ("region-card"
                ++ (if index == model.selected then
                        " region-card--selected"

                    else
                        ""
                   )
            )
        , onClick (Select index)
        ]
        [ div [ class "region-card__heading" ]
            [ h3 [ class "region-card__title" ] [ text (String.fromInt (index + 1) ++ ". " ++ region.regionType) ]
            , strong []
                [ text
                    (if region.keep then
                        "KEPT"

                     else
                        "DELETED"
                    )
                ]
            ]
        , p [] [ text (Timeline.timestamp region.start ++ " – " ++ Timeline.timestamp region.end) ]
        , div [ class "region-card__controls" ]
            [ button [ Ui.button, onClick (Toggle index), disabled (busy model) ]
                [ text
                    (if region.keep then
                        "Delete Section"

                     else
                        "Keep Section"
                    )
                ]
            , button [ Ui.button, onClick (Play index) ] [ text "Play Region" ]
            ]
        , if index > 0 then
            nudgeControls model index

          else
            text ""
        , if index < List.length model.regions - 1 then
            nudgeControls model (index + 1)

          else
            text ""
        ]


nudgeControls model boundary =
    div [ class "boundary-controls" ] [ span [] [ text ("Boundary " ++ String.fromInt boundary ++ ":") ], button [ Ui.button, onClick (Nudge boundary -1), disabled (busy model) ] [ text "−1s" ], button [ Ui.button, onClick (Nudge boundary -0.1), disabled (busy model) ] [ text "−0.1s" ], button [ Ui.button, onClick (Nudge boundary 0.1), disabled (busy model) ] [ text "+0.1s" ], button [ Ui.button, onClick (Nudge boundary 1), disabled (busy model) ] [ text "+1s" ] ]


busy model =
    model.applying || (model.sermon.stage == "edit" && (model.sermon.status == "pending" || model.sermon.status == "running"))


loadFailed model =
    model.error /= Nothing && (model.waveform == Nothing || model.analysis == Nothing)


viewTimelineControls model =
    let
        duration =
            model.waveform |> Maybe.map .duration |> Maybe.withDefault 0

        span =
            visibleSpan model

        viewEnd =
            min duration (model.viewStart + span)
    in
    div [ class "timeline-controls" ]
        [ div [ class "timeline-controls__buttons" ]
            [ button [ Ui.button, onClick ZoomOut, disabled (model.zoom <= 1) ] [ text "Zoom Out" ]
            , button [ Ui.button, onClick ZoomIn, disabled (model.zoom >= 64) ] [ text "Zoom In" ]
            , button [ Ui.button, onClick (Pan -1), disabled (model.viewStart <= 0) ] [ text "Earlier" ]
            , button [ Ui.button, onClick (Pan 1), disabled (viewEnd >= duration) ] [ text "Later" ]
            , button [ Ui.button, onClick ShowAll, disabled (model.zoom <= 1) ] [ text "Show All" ]
            ]
        , p [ class "timeline-controls__range" ]
            [ text ("Showing " ++ Timeline.timestamp model.viewStart ++ " – " ++ Timeline.timestamp viewEnd ++ " of " ++ Timeline.timestamp duration) ]
        ]


redraw model =
    ( model, Cmd.none, renderEffect model )


setZoom requested model =
    let
        duration =
            model.waveform |> Maybe.map .duration |> Maybe.withDefault 0

        oldSpan =
            visibleSpan model

        center =
            model.viewStart + oldSpan / 2

        zoom =
            max 1 (min 64 requested)

        newSpan =
            if zoom <= 0 then
                duration

            else
                duration / zoom

        next =
            { model | zoom = zoom }
    in
    { next | viewStart = clampViewStart next (center - newSpan / 2) }


visibleSpan model =
    let
        duration =
            model.waveform |> Maybe.map .duration |> Maybe.withDefault 0
    in
    if model.zoom <= 0 then
        duration

    else
        duration / model.zoom


clampViewStart model requested =
    let
        duration =
            model.waveform |> Maybe.map .duration |> Maybe.withDefault 0
    in
    max 0 (min (max 0 (duration - visibleSpan model)) requested)
